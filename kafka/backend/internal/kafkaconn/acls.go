package kafkaconn

// acls.go：ACL 管理（契约 §5.2，逻辑对照 tiny-rdm kafka_service.go
// ListACLs :800 / CreateACL :831 / DeleteACL :873 / kafkaACLBuilder :3071
// 及 acl 归一化族 :3093-3443 收敛重写）。
// 过宽拒绝（IMPL_PLAN §5.2 filter{} 拒绝过宽）：list/delete 要求
// resourceType 与 operation 至少显式给出其一之外，还要求过滤条件非全空
// —— 实现取 resourceType 必填（tinyrdm validateExactKafkaACLFilter 同义）。

import (
	"context"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// ListACLs 实现 kafka/acls/list。
func (s *Service) ListACLs(ctx context.Context, req ACLsListRequest) (*ACLsListResult, error) {
	builder, err := aclBuilderFromFilter(req.Filter, false)
	if err != nil {
		return nil, err
	}
	var result ACLsListResult
	err = s.withAdmin(req.ConnectionID, func(client *kgo.Client) error {
		admin := kadm.NewClient(client)
		ctx, cancel := context.WithTimeout(ctx, adminTimeout)
		defer cancel()

		described, err := admin.DescribeACLs(ctx, builder)
		if err != nil {
			return err
		}
		result.ACLs = []ACLBinding{}
		for _, filterResult := range described {
			if filterResult.Err != nil {
				result.ACLs = append(result.ACLs, ACLBinding{Error: filterResult.Err.Error()})
				continue
			}
			for _, acl := range filterResult.Described {
				result.ACLs = append(result.ACLs, aclBindingFromDescribed(acl))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// CreateACL 实现 kafka/acls/create（写门禁）。
func (s *Service) CreateACL(ctx context.Context, req ACLsCreateRequest) error {
	profile := s.profileOf(req.ConnectionID)
	target := aclTarget(req.ACL)
	if err := ensureWriteAllowed(profile, "acls/create"); err != nil {
		s.emitAudit(req.ConnectionID, "acls-create", target, "blocked", err.Error())
		return err
	}
	if req.ACL.ResourceName == "" {
		return errf("acl.resourceName is required")
	}
	if req.ACL.Principal == "" {
		return errf("acl.principal is required")
	}
	builder, err := aclBuilderFromACL(req.ACL, true)
	if err != nil {
		return err
	}

	err = s.withAdmin(req.ConnectionID, func(client *kgo.Client) error {
		admin := kadm.NewClient(client)
		ctx, cancel := context.WithTimeout(ctx, adminTimeout)
		defer cancel()

		results, err := admin.CreateACLs(ctx, builder)
		if err != nil {
			return err
		}
		for _, result := range results {
			if result.Err != nil {
				return result.Err
			}
		}
		return nil
	})
	if err != nil {
		s.emitAudit(req.ConnectionID, "acls-create", target, "error", err.Error())
		return err
	}
	s.emitAudit(req.ConnectionID, "acls-create", target, "success", "")
	return nil
}

// DeleteACLs 实现 kafka/acls/delete（critical 门禁）。
func (s *Service) DeleteACLs(ctx context.Context, req ACLsDeleteRequest) (*ACLsDeleteResult, error) {
	profile := s.profileOf(req.ConnectionID)
	if err := ensureDeleteAllowed(profile, "acls/delete"); err != nil {
		s.emitAudit(req.ConnectionID, "acls-delete", aclFilterTarget(req.Filter), "blocked", err.Error())
		return nil, err
	}
	builder, err := aclBuilderFromFilter(req.Filter, false)
	if err != nil {
		return nil, err
	}
	var result ACLsDeleteResult
	err = s.withAdmin(req.ConnectionID, func(client *kgo.Client) error {
		admin := kadm.NewClient(client)
		ctx, cancel := context.WithTimeout(ctx, adminTimeout)
		defer cancel()

		deleted, err := admin.DeleteACLs(ctx, builder)
		if err != nil {
			return err
		}
		result.Matched = []ACLBinding{}
		for _, filterResult := range deleted {
			if filterResult.Err != nil {
				result.Matched = append(result.Matched, ACLBinding{Error: filterResult.Err.Error()})
				continue
			}
			for _, acl := range filterResult.Deleted {
				item := ACLBinding{
					ResourceType: acl.Type.String(),
					ResourceName: acl.Name,
					PatternType:  acl.Pattern.String(),
					Principal:    acl.Principal,
					Host:         acl.Host,
					Operation:    acl.Operation.String(),
					Permission:   acl.Permission.String(),
				}
				if acl.Err != nil {
					item.Error = acl.Err.Error()
				}
				result.Matched = append(result.Matched, item)
			}
		}
		return nil
	})
	if err != nil {
		s.emitAudit(req.ConnectionID, "acls-delete", aclFilterTarget(req.Filter), "error", err.Error())
		return nil, err
	}
	s.emitAudit(req.ConnectionID, "acls-delete", aclFilterTarget(req.Filter), "success", "")
	return &result, nil
}

// aclBuilderFromFilter 组装 describe/delete 过滤 builder（tinyrdm
// applyKafkaACLAllowFilter 语义：空 principal/host = 任意）。
func aclBuilderFromFilter(filter ACLFilter, create bool) (*kadm.ACLBuilder, error) {
	resourceType, err := aclResourceType(filter.ResourceType)
	if err != nil {
		return nil, err
	}
	if resourceType == kmsg.ACLResourceTypeAny && trimSpace(filter.ResourceName) == "" {
		return nil, errf("filter is too broad: resourceType (or resourceName) is required")
	}
	if resourceType == kmsg.ACLResourceTypeAny {
		// 只给了 resourceName：仍要求 principal 或 operation 至少一项限定，
		// 避免全库扫 ACL。
		if trimSpace(filter.Principal) == "" && trimSpace(filter.Operation) == "" {
			return nil, errf("filter is too broad: principal or operation is required alongside resourceName")
		}
	}

	operation, err := aclOperationType(filter.Operation)
	if err != nil {
		return nil, err
	}
	pattern, err := aclPatternType(filter.PatternType)
	if err != nil {
		return nil, err
	}
	permission, err := aclPermissionType(filter.Permission)
	if err != nil {
		return nil, err
	}

	builder := kadm.NewACLs()
	applyACLResource(builder, resourceType, filter.ResourceName, pattern)
	builder.Operations(operation)
	applyACLPermission(builder, permission, filter.Principal, filter.Host)
	return builder, nil
}

// aclBuilderFromACL 组装 create builder（必填校验 + 默认 literal/allow）。
func aclBuilderFromACL(acl ACLBinding, create bool) (*kadm.ACLBuilder, error) {
	resourceType, err := aclResourceType(acl.ResourceType)
	if err != nil {
		return nil, err
	}
	if create && resourceType == kmsg.ACLResourceTypeAny {
		return nil, errf("acl.resourceType must be a concrete type (not any)")
	}
	if create && trimSpace(acl.ResourceName) == "" {
		return nil, errf("acl.resourceName is required")
	}
	operation, err := aclOperationType(acl.Operation)
	if err != nil {
		return nil, err
	}
	if create && operation == kmsg.ACLOperationAny {
		return nil, errf("acl.operation must be a concrete operation (not any)")
	}
	pattern, err := aclPatternType(acl.PatternType)
	if err != nil {
		return nil, err
	}
	permission, err := aclPermissionType(acl.Permission)
	if err != nil {
		return nil, err
	}
	if trimSpace(acl.Principal) == "" {
		return nil, errf("acl.principal is required")
	}

	builder := kadm.NewACLs()
	applyACLResource(builder, resourceType, acl.ResourceName, pattern)
	builder.Operations(operation)
	applyACLPermission(builder, permission, acl.Principal, acl.Host)
	return builder, nil
}

// applyACLResource 按资源类型挂载（builder 方法族不可参数化，逐类分发）。
func applyACLResource(builder *kadm.ACLBuilder, resourceType kmsg.ACLResourceType, name string, pattern kmsg.ACLResourcePatternType) {
	builder.ResourcePatternType(pattern)
	switch resourceType {
	case kmsg.ACLResourceTypeTopic:
		builder.Topics(name)
	case kmsg.ACLResourceTypeGroup:
		builder.Groups(name)
	case kmsg.ACLResourceTypeCluster:
		builder.Clusters()
	case kmsg.ACLResourceTypeTransactionalId:
		builder.TransactionalIDs(name)
	case kmsg.ACLResourceTypeDelegationToken:
		builder.DelegationTokens(name)
	default: // any / user
		builder.AnyResource(name)
	}
}

// applyACLPermission 权限与 principal/host（空 = 任意，tinyrdm 同款）。
func applyACLPermission(builder *kadm.ACLBuilder, permission kmsg.ACLPermissionType, principal, host string) {
	switch permission {
	case kmsg.ACLPermissionTypeDeny:
		builder.MaybeDeny(principal)
		builder.MaybeDenyHosts(host)
	default:
		builder.MaybeAllow(principal)
		builder.MaybeAllowHosts(host)
	}
	builder.PrefixUserExcept("User:", "Group:", "ANONYMOUS")
}

func aclBindingFromDescribed(acl kadm.DescribedACL) ACLBinding {
	return ACLBinding{
		ResourceType: acl.Type.String(),
		ResourceName: acl.Name,
		PatternType:  acl.Pattern.String(),
		Principal:    acl.Principal,
		Host:         acl.Host,
		Operation:    acl.Operation.String(),
		Permission:   acl.Permission.String(),
	}
}

func aclTarget(acl ACLBinding) string {
	return acl.ResourceType + "/" + acl.ResourceName + "/" + acl.Principal
}

func aclFilterTarget(filter ACLFilter) string {
	return aclTarget(ACLBinding{
		ResourceType: filter.ResourceType,
		ResourceName: filter.ResourceName,
		Principal:    filter.Principal,
	})
}
