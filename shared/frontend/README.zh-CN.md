# shared/frontend — 插件前端宿主适配公共层

宿主桥/宿主特性的**前端适配公共代码唯一实现点**。三插件（ssh / files / ldap）
前端一律以相对路径引用这里的模块，**禁止在插件内各抄一份**（背景：binary
事件契约变更曾在 ssh/files 各落一份相同修复，收敛后单点演进）。

## 引用方式

插件前端源码相对引用（Vite / vue-tsc 均无需额外配置，tsconfig 随 import
自动纳入检查）：

```ts
// 位于 <plugin>/frontend/src/App.vue：
import { bridgeBinaryBytes } from "../../shared/frontend/binaryEvent";
// 位于 <plugin>/frontend/src/lib/*.spec.ts：
import { bridgeBinaryBytes } from "../../../shared/frontend/binaryEvent";
```

## 约定

- 每个插件保留薄 spec 引用 shared 模块跑断言（证明该插件工具链下 import
  解析、打包、行为都成立）；实现与文档只在 shared 维护。
- 收敛候选：宿主桥 binary 事件（binaryEvent.ts）、主题令牌兜底、
  workbenchState/fileTransfer 降级 shim、七语工具等跨插件同构逻辑。
- 宿主契约变更的发现与跟进流程见 `AGENTS.md` 硬性规则 7 与
  `scripts/host-sync.sh contract`。

## 现有模块

| 模块 | 用途 | 接入方 |
| --- | --- | --- |
| `binaryEvent.ts` | 宿主桥 binary 事件双形状归一化（`data: Uint8Array` / 旧 `dataBase64`） | ssh（终端输出帧、SFTP 下载分块）、files（files/download 分块） |
| `themeSync.ts` | 宿主主题令牌 → 插件 CSS 变量桥（`themeBridgeCss` / `installHostThemeBridge`）：把 `--background` 等插件变量声明为宿主 `--color-*`/`--radius-*`/`--font-*` 令牌的引用，首绘即命中宿主主题、主题变化经 SDK 令牌更新自动跟随；含 `data-dbx-theme` color-scheme 同步。宿主无令牌时回退暗色规范值（optional 降级） | ssh / ldap / files / kafka（`main.ts` 挂载前 `installHostThemeBridge()`，各带薄 spec） |
