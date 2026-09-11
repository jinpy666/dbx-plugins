// @vitest-environment happy-dom
// AssociationPanel（条目关联视图）workbench UI tests。面板重构为「本地过滤框 +
// 四子页签（成员 / DN 引用 / 所属 / 被引用，带计数徽标）+ 激活列表区（一次只
// 显示一个列表、两行式表格行）」，本套用例覆盖：
// - 子页签：四页签渲染、默认激活成员、互斥切换、计数徽标（成员 / DN 引用为
//   同步计数恒显示；所属 / 被引用在反查完成前不显示数字）；
// - 本地过滤：子串收敛、大小写不敏感、无匹配 noMatch 空态、清空恢复、作用于
//   反查列表、dn 变化清空过滤词；
// - 成员区：直读 member / uniqueMember 属性（大小写不敏感、空态归一）；
// - DN 引用区：按属性正查归一为扁平行（行内保留来源属性名替代旧分组小标题、
//   schema 表优先、内置核心表兜底、member/uniqueMember 不重复）；
// - 所属区：active 惰性反查（过滤器转义、失败重试、截断提示、空态、dn 变化
//   重缓存）、被引用区：单次 OR 过滤器反查（同款门控与防竞态）；
// - 行点击 openEntry、行尾复制按钮。
// ldapApi 在文件内 mock，不涉及宿主桥或网络；VirtualList 在 happy-dom 里视口
// 高度恒为 0（computeWindow 退化为空窗口），故 stub 成平铺渲染——窗口数学已由
// virtualScroll.spec.ts 覆盖。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { defineComponent, h, type PropType } from "vue";
import { ldapApi, type LdapSearchResult } from "../lib/api";

vi.mock("../lib/api", () => ({
  ldapApi: {
    search: vi.fn(),
  },
}));

import AssociationPanel from "./AssociationPanel.vue";

const searchMock = vi.mocked(ldapApi.search);

// 调用计数跨用例累积会让 toHaveBeenCalledTimes 误报：每例前清空 mock。
beforeEach(() => {
  searchMock.mockReset();
});

// VirtualList stub：按 items 平铺渲染默认 slot（保持行选择器语义可查）。
const VirtualListStub = defineComponent({
  name: "VirtualList",
  props: { items: { type: Array as PropType<unknown[]>, required: true } },
  setup(props, { slots }) {
    return () =>
      h(
        "div",
        { class: "vlist" },
        props.items.map((item, index) => h("div", { class: "vlist-row", key: index }, slots.default?.({ item, index }))),
      );
  },
});

const BASE_DN = "dc=demo,dc=dbx";
const GROUP_DN = "cn=team-a,ou=groups,dc=demo,dc=dbx";
const MEMBER_DNS = [
  "uid=user0000,ou=people,dc=demo,dc=dbx",
  "uid=user0001,ou=people,dc=demo,dc=dbx",
  "uid=user0002,ou=people,dc=demo,dc=dbx",
];

function mountPanel(props: { dn: string; attributes: Record<string, string[]>; baseDn?: string; active?: boolean; dnAttributes?: string[] }) {
  return mount(AssociationPanel, {
    props: { baseDn: BASE_DN, active: false, ...props },
    global: { stubs: { VirtualList: VirtualListStub } },
  });
}

// -- 子页签定位 helpers --------------------------------------------------------

type PanelWrapper = ReturnType<typeof mountPanel>;

// 页签按钮（.mode-switch 内，data-tab 标识四个页签）。
const tabButton = (wrapper: PanelWrapper, key: string) => wrapper.find(`.assoc-tabs [data-tab="${key}"]`);
// 切换激活页签（列表区互斥切换由组件内部响应）。
async function switchTab(wrapper: PanelWrapper, key: string) {
  await tabButton(wrapper, key).trigger("click");
}

// -- 列表区行 helpers（激活页签内唯一渲染的区块） -------------------------------

const memberRows = (wrapper: PanelWrapper) => wrapper.findAll(".assoc-members .assoc-row");
const referencesRows = (wrapper: PanelWrapper) => wrapper.findAll(".assoc-references .assoc-row");
const memberOfRows = (wrapper: PanelWrapper) => wrapper.findAll(".assoc-member-of .assoc-row");
const referencedByRows = (wrapper: PanelWrapper) => wrapper.findAll(".assoc-referenced-by .assoc-row");

// -- 搜索 mock helpers ---------------------------------------------------------

// 面板激活后所属/被引用两个反查共用 ldapApi.search：按过滤器前缀筛调用，
// 断言互不串扰（memberOf 恒以 "(member=" 开头，被引用 OR 过滤器以 "(|(" 开头）。
const callsWithFilterPrefix = (prefix: string) => searchMock.mock.calls.filter(([request]) => request.filter.startsWith(prefix));

// 被引用反查过滤器（DN_REFERENCE_CORE 顺序）：值转义后逐属性等值 OR。
const referencedByFilter = (value: string) =>
  `(|(managedBy=${value})(owner=${value})(secretary=${value})(assistant=${value})(manager=${value})(seeAlso=${value})(altRecipient=${value}))`;

// 受控 promise：手动放行某个前缀的反查响应（徽标"未完成→完成"时序断言用）。
function gateSearch() {
  const gates: Array<(result: LdapSearchResult) => void> = [];
  searchMock.mockImplementation(
    () =>
      new Promise<LdapSearchResult>((resolve) => {
        gates.push(resolve);
      }),
  );
  // 按过滤器前缀找到对应在途请求并放行结果。
  const resolveFor = (prefix: string, result: LdapSearchResult) => {
    const index = searchMock.mock.calls.findIndex(([request]) => request.filter.startsWith(prefix));
    gates[index](result);
  };
  return { resolveFor };
}

describe("AssociationPanel sub-tabs", () => {
  it("renders four tabs; sync counts always shown, reverse-lookup counts only after done", async () => {
    const { resolveFor } = gateSearch();
    const wrapper = mountPanel({
      dn: MEMBER_DNS[0],
      attributes: { member: MEMBER_DNS, manager: [GROUP_DN] },
      active: true,
      dnAttributes: ["manager"],
    });
    // 四个子页签都渲染，默认激活「成员」
    expect(wrapper.findAll(".assoc-tabs [data-tab]")).toHaveLength(4);
    expect(tabButton(wrapper, "members").classes()).toContain("is-active");
    expect(tabButton(wrapper, "references").classes()).not.toContain("is-active");
    expect(tabButton(wrapper, "memberOf").classes()).not.toContain("is-active");
    expect(tabButton(wrapper, "referencedBy").classes()).not.toContain("is-active");
    // 同步计数恒显示（成员 3 条、DN 引用 1 条）
    expect(tabButton(wrapper, "members").find(".tree-badge").text()).toBe("3");
    expect(tabButton(wrapper, "references").find(".tree-badge").text()).toBe("1");
    // 两个反查尚未完成：不显示数字
    expect(tabButton(wrapper, "memberOf").find(".tree-badge").exists()).toBe(false);
    expect(tabButton(wrapper, "referencedBy").find(".tree-badge").exists()).toBe(false);
    // 反查完成后徽标出现并随结果更新
    resolveFor("(member=", { entries: [{ dn: GROUP_DN, attributes: {} }], count: 1, truncated: false });
    resolveFor("(|(", {
      entries: [
        { dn: GROUP_DN, attributes: {} },
        { dn: "ou=x,dc=demo,dc=dbx", attributes: {} },
      ],
      count: 2,
      truncated: false,
    });
    await flushPromises();
    expect(tabButton(wrapper, "memberOf").find(".tree-badge").text()).toBe("1");
    expect(tabButton(wrapper, "referencedBy").find(".tree-badge").text()).toBe("2");
  });

  it("switches lists exclusively; only the active tab's list section is rendered", async () => {
    searchMock.mockResolvedValue({ entries: [{ dn: GROUP_DN, attributes: {} }], count: 1, truncated: false });
    const wrapper = mountPanel({ dn: MEMBER_DNS[0], attributes: { member: MEMBER_DNS }, active: true });
    await flushPromises();
    // 默认成员页签：成员区在、其余不在
    expect(wrapper.find(".assoc-members").exists()).toBe(true);
    expect(wrapper.find(".assoc-references").exists()).toBe(false);
    expect(wrapper.find(".assoc-member-of").exists()).toBe(false);
    expect(wrapper.find(".assoc-referenced-by").exists()).toBe(false);
    // 切到所属：互斥
    await switchTab(wrapper, "memberOf");
    expect(wrapper.find(".assoc-members").exists()).toBe(false);
    expect(wrapper.find(".assoc-member-of").exists()).toBe(true);
    // 切到 DN 引用（该条目无 DN 引用属性 → 空态）
    await switchTab(wrapper, "references");
    expect(wrapper.find(".assoc-member-of").exists()).toBe(false);
    expect(wrapper.find(".assoc-references .empty").text()).toBe("该条目没有 DN 引用属性。");
  });
});

describe("AssociationPanel local filter", () => {
  it("narrows the active members list case-insensitively, shows noMatch, restores on clear", async () => {
    const wrapper = mountPanel({ dn: GROUP_DN, attributes: { member: MEMBER_DNS } });
    // 大小写不敏感子串匹配（RDN 即完整 DN 的一部分）
    await wrapper.find(".assoc-filter").setValue("USER0001");
    const rows = memberRows(wrapper);
    expect(rows).toHaveLength(1);
    expect(rows[0].text()).toContain("user0001");
    expect(rows[0].attributes("title")).toBe(MEMBER_DNS[1]);
    // 无匹配 → noMatch 空态
    await wrapper.find(".assoc-filter").setValue("no-such-dn");
    expect(wrapper.find(".assoc-members .empty").text()).toBe("没有匹配的条目。");
    expect(memberRows(wrapper)).toHaveLength(0);
    // 清空（含纯空白）恢复全部
    await wrapper.find(".assoc-filter").setValue("  ");
    expect(memberRows(wrapper)).toHaveLength(3);
    expect(wrapper.find(".assoc-members .empty").exists()).toBe(false);
  });

  it("applies to the active reverse-lookup list and resets the keyword when dn changes", async () => {
    searchMock.mockImplementation(async (request) =>
      request.filter.startsWith("(member=")
        ? { entries: [{ dn: GROUP_DN, attributes: {} }], count: 1, truncated: false }
        : { entries: [], count: 0, truncated: false },
    );
    const wrapper = mountPanel({ dn: MEMBER_DNS[0], attributes: {}, active: true });
    await flushPromises();
    await switchTab(wrapper, "memberOf");
    expect(memberOfRows(wrapper)).toHaveLength(1);
    // 过滤词作用于所属列表：命中完整 DN 子串
    await wrapper.find(".assoc-filter").setValue("team");
    expect(memberOfRows(wrapper)).toHaveLength(1);
    await wrapper.find(".assoc-filter").setValue("zzz");
    expect(wrapper.find(".assoc-member-of .empty").text()).toBe("没有匹配的条目。");
    // dn 变化：过滤词清空（新 dn 的反查结果全量展示）
    await wrapper.setProps({ dn: MEMBER_DNS[1] });
    expect((wrapper.find(".assoc-filter").element as HTMLInputElement).value).toBe("");
    await flushPromises();
    expect(memberOfRows(wrapper)).toHaveLength(1);
  });
});

describe("AssociationPanel members tab", () => {
  it("renders member values as two-line rows that emit openEntry with the full DN and badge count", async () => {
    const wrapper = mountPanel({ dn: GROUP_DN, attributes: { member: MEMBER_DNS, cn: ["team-a"] } });
    const rows = memberRows(wrapper);
    expect(rows).toHaveLength(3);
    // 两行式：第一行 RDN 为主文本、完整 DN 随行展示，title 悬停看全量
    expect(rows[0].text()).toContain("uid=user0000");
    expect(rows[0].text()).toContain(MEMBER_DNS[0]);
    expect(rows[0].attributes("title")).toBe(MEMBER_DNS[0]);
    await rows[1].trigger("click");
    expect(wrapper.emitted("openEntry")?.[0]).toEqual([MEMBER_DNS[1]]);
    // 页签徽标计数 = member 值条数（title 复用既有 count 文案）
    const badge = tabButton(wrapper, "members").find(".tree-badge");
    expect(badge.text()).toBe("3");
    expect(badge.attributes("title")).toBe("3 个条目");
  });

  it("shows the same empty copy when the attribute is absent or present but empty", () => {
    const emptyCopy = "该条目没有 member 属性（可能不是组，或组为空）。";
    const withoutAttr = mountPanel({ dn: "uid=user0000,ou=people,dc=demo,dc=dbx", attributes: { cn: ["User 0"] } });
    expect(withoutAttr.find(".assoc-members .empty").text()).toBe(emptyCopy);
    const withEmptyAttr = mountPanel({ dn: "cn=empty,ou=groups,dc=demo,dc=dbx", attributes: { member: [] } });
    expect(withEmptyAttr.find(".assoc-members .empty").text()).toBe(emptyCopy);
  });

  it("falls back to uniqueMember and matches attribute names case-insensitively", () => {
    const wrapper = mountPanel({
      dn: "cn=group,ou=groups,dc=demo,dc=dbx",
      attributes: { UniqueMember: ["uid=bob,ou=people,dc=demo,dc=dbx"] },
    });
    const rows = memberRows(wrapper);
    expect(rows).toHaveLength(1);
    expect(rows[0].text()).toContain("uid=bob");
    expect(rows[0].attributes("title")).toBe("uid=bob,ou=people,dc=demo,dc=dbx");
  });
});

describe("AssociationPanel dn-references tab", () => {
  const USER0 = MEMBER_DNS[0];
  const SERVICES_DN = `ou=services,${BASE_DN}`;

  it("flattens groups into rows that keep the source attribute inline and emits openEntry", async () => {
    const wrapper = mountPanel({
      dn: SERVICES_DN,
      attributes: { managedBy: [USER0], seeAlso: [GROUP_DN] },
      dnAttributes: ["managedBy", "seeAlso"],
    });
    await switchTab(wrapper, "references");
    const rows = referencesRows(wrapper);
    expect(rows).toHaveLength(2);
    // 行内保留来源属性名（替代旧分组小标题），保留条目返回的原始大小写
    expect(rows[0].find(".assoc-attr").text()).toBe("managedBy");
    expect(rows[0].text()).toContain("uid=user0000");
    expect(rows[0].attributes("title")).toBe(USER0);
    expect(rows[1].find(".assoc-attr").text()).toBe("seeAlso");
    await rows[1].trigger("click");
    expect(wrapper.emitted("openEntry")?.[0]).toEqual([GROUP_DN]);
    // 页签徽标 = 各 DN 值属性值总数
    expect(tabButton(wrapper, "references").find(".tree-badge").text()).toBe("2");
  });

  it("never repeats member/uniqueMember in the references tab", async () => {
    const wrapper = mountPanel({
      dn: GROUP_DN,
      attributes: { member: MEMBER_DNS, manager: [USER0] },
      dnAttributes: ["member", "uniqueMember", "manager"],
    });
    // 成员页签不受影响；DN 引用页签只剩 manager（member 专属值 user0002 不出现）
    expect(memberRows(wrapper)).toHaveLength(3);
    await switchTab(wrapper, "references");
    const rows = referencesRows(wrapper);
    expect(rows).toHaveLength(1);
    expect(rows[0].find(".assoc-attr").text()).toBe("manager");
    expect(wrapper.find(".assoc-references").text()).not.toContain("uid=user0002");
  });

  it("falls back to the built-in core table when dnAttributes is missing or empty", async () => {
    // 缺省 prop：核心表命中 managedBy，识别表外的属性不展示
    const withoutProp = mountPanel({
      dn: SERVICES_DN,
      attributes: { managedBy: [USER0], deptNumber: ["D1"] },
    });
    await switchTab(withoutProp, "references");
    const rows = referencesRows(withoutProp);
    expect(rows).toHaveLength(1);
    expect(rows[0].find(".assoc-attr").text()).toBe("managedBy");
    withoutProp.unmount();
    // 空数组同样走核心表兜底
    const withEmptyProp = mountPanel({
      dn: SERVICES_DN,
      attributes: { managedBy: [USER0] },
      dnAttributes: [],
    });
    await switchTab(withEmptyProp, "references");
    expect(referencesRows(withEmptyProp)).toHaveLength(1);
  });

  it("matches attribute names case-insensitively and keeps the entry's casing inline", async () => {
    const wrapper = mountPanel({
      dn: SERVICES_DN,
      attributes: { ManagedBy: [USER0] },
      dnAttributes: ["MANAGEDBY"],
    });
    await switchTab(wrapper, "references");
    const rows = referencesRows(wrapper);
    expect(rows).toHaveLength(1);
    expect(rows[0].find(".assoc-attr").text()).toBe("ManagedBy");
    expect(rows[0].attributes("title")).toBe(USER0);
  });

  it("shows the empty copy when no DN-valued attribute carries values", async () => {
    const wrapper = mountPanel({
      dn: "uid=user0000,ou=people,dc=demo,dc=dbx",
      attributes: { cn: ["User 0"] },
      dnAttributes: ["managedBy"],
    });
    await switchTab(wrapper, "references");
    expect(wrapper.find(".assoc-references .empty").text()).toBe("该条目没有 DN 引用属性。");
  });
});

describe("AssociationPanel member-of tab", () => {
  it("searches lazily only when active, with the equality filter over the entry DN", async () => {
    searchMock.mockResolvedValue({ entries: [{ dn: GROUP_DN, attributes: {} }], count: 1, truncated: false });
    const dn = MEMBER_DNS[0];
    // active=false：从不发起（所属/被引用两区同门控）
    const idle = mountPanel({ dn, attributes: {}, active: false });
    await flushPromises();
    expect(searchMock).not.toHaveBeenCalled();
    idle.unmount();
    // active=true：所属区发起一次（被引用区另计），参数完整
    const wrapper = mountPanel({ dn, attributes: {}, active: true });
    await flushPromises();
    const memberCalls = callsWithFilterPrefix("(member=");
    expect(memberCalls).toHaveLength(1);
    const request = memberCalls[0][0];
    expect(request.filter).toBe(`(member=${dn})`);
    expect(request.scope).toBe("sub");
    expect(request.baseDn).toBe(BASE_DN);
    expect(request.attributes).toEqual(["1.1"]);
    expect(request.sizeLimit).toBe(1000);
    // 切到所属页签：结果渲染为可点击行
    await switchTab(wrapper, "memberOf");
    const rows = memberOfRows(wrapper);
    expect(rows).toHaveLength(1);
    await rows[0].trigger("click");
    expect(wrapper.emitted("openEntry")?.[0]).toEqual([GROUP_DN]);
    // 完成后页签徽标出现计数
    expect(tabButton(wrapper, "memberOf").find(".tree-badge").text()).toBe("1");
  });

  it("escapes RFC 4515 special characters in the DN filter value", async () => {
    searchMock.mockResolvedValue({ entries: [], count: 0, truncated: false });
    const dn = "cn=a(b),ou=groups,dc=demo,dc=dbx";
    mountPanel({ dn, attributes: {}, active: true });
    await flushPromises();
    expect(callsWithFilterPrefix("(member=")[0][0].filter).toBe("(member=cn=a\\28b\\29,ou=groups,dc=demo,dc=dbx)");
  });

  it("shows the failure copy with retry and recovers after a retry succeeds", async () => {
    // 按过滤器分派：仅首次所属反查失败，其余调用（含被引用区）正常返回，
    // 保证 error / 重试断言只针对本区。
    let memberOfAttempts = 0;
    searchMock.mockImplementation(async (request) => {
      if (request.filter.startsWith("(member=")) {
        memberOfAttempts += 1;
        if (memberOfAttempts === 1) throw new Error("boom");
        return { entries: [{ dn: GROUP_DN, attributes: {} }], count: 1, truncated: false };
      }
      return { entries: [], count: 0, truncated: false };
    });
    const wrapper = mountPanel({ dn: MEMBER_DNS[0], attributes: {}, active: true });
    await flushPromises();
    // 行内错误态 + 上抛 error（loadFailed 文案已泛化为"关联搜索失败"）
    expect(wrapper.emitted("error")?.[0]).toEqual(["关联搜索失败：boom"]);
    await switchTab(wrapper, "memberOf");
    const section = wrapper.find(".assoc-member-of");
    expect(section.text()).toContain("关联搜索失败：boom");
    // 重试按钮点击后重新搜索并恢复渲染
    await section.find(".assoc-retry").trigger("click");
    await flushPromises();
    expect(callsWithFilterPrefix("(member=")).toHaveLength(2);
    expect(wrapper.emitted("error")).toHaveLength(1);
    expect(memberOfRows(wrapper)).toHaveLength(1);
    expect(wrapper.find(".assoc-member-of .form-error").exists()).toBe(false);
  });

  it("shows the truncation hint with the limit value when truncated=true", async () => {
    searchMock.mockResolvedValue({ entries: [{ dn: GROUP_DN, attributes: {} }], count: 1, truncated: true });
    const wrapper = mountPanel({ dn: MEMBER_DNS[0], attributes: {}, active: true });
    await flushPromises();
    await switchTab(wrapper, "memberOf");
    expect(wrapper.find(".assoc-member-of").text()).toContain("结果已达上限 1000");
    // 有结果时不再显示空态文案
    expect(wrapper.find(".assoc-member-of .empty").exists()).toBe(false);
  });

  it("shows the empty copy when no entries reference the DN", async () => {
    searchMock.mockResolvedValue({ entries: [], count: 0, truncated: false });
    const wrapper = mountPanel({ dn: MEMBER_DNS[0], attributes: {}, active: true });
    await flushPromises();
    await switchTab(wrapper, "memberOf");
    expect(wrapper.find(".assoc-member-of .empty").text()).toBe("没有条目的 member 属性引用此 DN。");
    expect(wrapper.find(".assoc-member-of .hint").exists()).toBe(false);
  });

  it("resets the cached result and re-searches with the new DN while active", async () => {
    searchMock.mockImplementation(async (request) =>
      request.filter.startsWith("(member=")
        ? { entries: [{ dn: GROUP_DN, attributes: {} }], count: 1, truncated: false }
        : { entries: [], count: 0, truncated: false },
    );
    const wrapper = mountPanel({ dn: MEMBER_DNS[0], attributes: {}, active: true });
    await flushPromises();
    await switchTab(wrapper, "memberOf");
    expect(memberOfRows(wrapper)).toHaveLength(1);
    // dn 变化：缓存作废（进入加载态），active 下立即用新 DN 重新搜索
    await wrapper.setProps({ dn: MEMBER_DNS[1] });
    expect(wrapper.find(".assoc-member-of").text()).toContain("搜索中…");
    await flushPromises();
    const memberCalls = callsWithFilterPrefix("(member=");
    expect(memberCalls).toHaveLength(2);
    expect(memberCalls[1][0].filter).toBe(`(member=${MEMBER_DNS[1]})`);
    expect(memberOfRows(wrapper)).toHaveLength(1);
  });
});

describe("AssociationPanel referenced-by tab", () => {
  const dn = MEMBER_DNS[0];

  it("searches lazily only when active with the OR filter over the core attributes and renders clickable rows", async () => {
    searchMock.mockResolvedValue({ entries: [{ dn: GROUP_DN, attributes: {} }], count: 1, truncated: false });
    // active=false：从不发起
    const idle = mountPanel({ dn, attributes: {}, active: false });
    await flushPromises();
    expect(callsWithFilterPrefix("(|(")).toHaveLength(0);
    idle.unmount();
    // active=true：发起一次单 OR 反查，参数完整
    const wrapper = mountPanel({ dn, attributes: {}, active: true });
    await flushPromises();
    const refCalls = callsWithFilterPrefix("(|(");
    expect(refCalls).toHaveLength(1);
    const request = refCalls[0][0];
    expect(request.filter).toBe(referencedByFilter(dn));
    expect(request.scope).toBe("sub");
    expect(request.baseDn).toBe(BASE_DN);
    expect(request.attributes).toEqual(["1.1"]);
    expect(request.sizeLimit).toBe(1000);
    // 切到被引用页签：结果渲染为可点击行
    await switchTab(wrapper, "referencedBy");
    const rows = referencedByRows(wrapper);
    expect(rows).toHaveLength(1);
    await rows[0].trigger("click");
    expect(wrapper.emitted("openEntry")?.[0]).toEqual([GROUP_DN]);
    expect(tabButton(wrapper, "referencedBy").find(".tree-badge").text()).toBe("1");
  });

  it("escapes RFC 4515 special characters in the reverse-lookup DN", async () => {
    searchMock.mockResolvedValue({ entries: [], count: 0, truncated: false });
    const weird = "cn=a(b),ou=people,dc=demo,dc=dbx";
    mountPanel({ dn: weird, attributes: {}, active: true });
    await flushPromises();
    expect(callsWithFilterPrefix("(|(")[0][0].filter).toBe(referencedByFilter("cn=a\\28b\\29,ou=people,dc=demo,dc=dbx"));
  });

  it("shows the failure copy with retry and recovers after a retry succeeds", async () => {
    // 按过滤器分派：仅首次被引用反查失败，其余调用正常返回。
    let refAttempts = 0;
    searchMock.mockImplementation(async (request) => {
      if (request.filter.startsWith("(|(")) {
        refAttempts += 1;
        if (refAttempts === 1) throw new Error("boom");
        return { entries: [{ dn: GROUP_DN, attributes: {} }], count: 1, truncated: false };
      }
      return { entries: [], count: 0, truncated: false };
    });
    const wrapper = mountPanel({ dn, attributes: {}, active: true });
    await flushPromises();
    expect(wrapper.emitted("error")?.[0]).toEqual(["关联搜索失败：boom"]);
    await switchTab(wrapper, "referencedBy");
    const section = wrapper.find(".assoc-referenced-by");
    expect(section.text()).toContain("关联搜索失败：boom");
    await section.find(".assoc-retry").trigger("click");
    await flushPromises();
    expect(callsWithFilterPrefix("(|(")).toHaveLength(2);
    expect(wrapper.find(".assoc-referenced-by .form-error").exists()).toBe(false);
    expect(referencedByRows(wrapper)).toHaveLength(1);
  });

  it("shows the truncation hint with the limit value when truncated=true", async () => {
    searchMock.mockImplementation(async (request) =>
      request.filter.startsWith("(|(")
        ? { entries: [{ dn: GROUP_DN, attributes: {} }], count: 1, truncated: true }
        : { entries: [], count: 0, truncated: false },
    );
    const wrapper = mountPanel({ dn, attributes: {}, active: true });
    await flushPromises();
    await switchTab(wrapper, "referencedBy");
    expect(wrapper.find(".assoc-referenced-by").text()).toContain("结果已达上限 1000");
    expect(wrapper.find(".assoc-referenced-by .empty").exists()).toBe(false);
  });

  it("shows the empty copy when no entries reference the DN", async () => {
    searchMock.mockResolvedValue({ entries: [], count: 0, truncated: false });
    const wrapper = mountPanel({ dn, attributes: {}, active: true });
    await flushPromises();
    await switchTab(wrapper, "referencedBy");
    expect(wrapper.find(".assoc-referenced-by .empty").text()).toBe("没有条目通过 managedBy/owner 等属性引用此 DN。");
    expect(wrapper.find(".assoc-referenced-by .hint").exists()).toBe(false);
  });

  it("resets the cached result and re-searches with the new DN while active", async () => {
    searchMock.mockImplementation(async (request) =>
      request.filter.startsWith("(|(")
        ? { entries: [{ dn: GROUP_DN, attributes: {} }], count: 1, truncated: false }
        : { entries: [], count: 0, truncated: false },
    );
    const wrapper = mountPanel({ dn, attributes: {}, active: true });
    await flushPromises();
    await switchTab(wrapper, "referencedBy");
    expect(referencedByRows(wrapper)).toHaveLength(1);
    // dn 变化：缓存作废（进入加载态），active 下立即用新 DN 重新搜索
    await wrapper.setProps({ dn: MEMBER_DNS[1] });
    expect(wrapper.find(".assoc-referenced-by").text()).toContain("搜索中…");
    await flushPromises();
    const refCalls = callsWithFilterPrefix("(|(");
    expect(refCalls).toHaveLength(2);
    expect(refCalls[1][0].filter).toBe(referencedByFilter(MEMBER_DNS[1]));
    expect(referencedByRows(wrapper)).toHaveLength(1);
  });
});

describe("AssociationPanel copy affordance", () => {
  afterEach(() => {
    delete (window as unknown as { dbxPlugin?: unknown }).dbxPlugin;
  });

  it("copies the full DN from a row and notifies honestly on success and failure", async () => {
    const writeText = vi.fn<(text: string) => Promise<void>>().mockResolvedValue(undefined);
    (window as unknown as { dbxPlugin?: unknown }).dbxPlugin = { clipboard: { writeText } };
    document.execCommand = () => false;
    const wrapper = mountPanel({ dn: GROUP_DN, attributes: { member: MEMBER_DNS } });
    // 行点击不应被复制按钮触发（stopPropagation）
    await wrapper.find(".assoc-members .assoc-copy").trigger("click");
    await flushPromises();
    expect(writeText).toHaveBeenLastCalledWith(MEMBER_DNS[0]);
    expect(wrapper.emitted("openEntry")).toBeUndefined();
    expect(wrapper.emitted("notify")?.at(-1)).toEqual(["已复制"]);
    // 桥写入失败如实反馈"复制失败"
    writeText.mockRejectedValue(new Error("boom"));
    await wrapper.find(".assoc-members .assoc-copy").trigger("click");
    await flushPromises();
    expect(wrapper.emitted("notify")?.at(-1)).toEqual(["复制失败"]);
  });
});
