package app

// GroupDirectoryReadError contains only closed, non-PII diagnostics. It never
// retains the provider response, URL, group identity, or underlying error text.
type GroupDirectoryReadError struct{ stage, reason string }

func NewGroupDirectoryReadError(stage, reason string) *GroupDirectoryReadError {
	switch stage {
	case "owner", "list", "detail", "pagination", "storage":
	default:
		stage = "list"
	}
	switch reason {
	case "provider_timeout", "provider_permission_denied", "provider_credentials_invalid", "provider_rate_limited", "provider_response_invalid", "owner_mismatch", "storage_unavailable":
	default:
		reason = "provider_unavailable"
	}
	return &GroupDirectoryReadError{stage: stage, reason: reason}
}
func (e *GroupDirectoryReadError) Error() string {
	return "group_directory_" + e.stage + "_" + e.reason
}
func (e *GroupDirectoryReadError) Unwrap() error { return ErrUnavailable }
func (e *GroupDirectoryReadError) Message() string {
	stage := map[string]string{"owner": "读取群主", "list": "读取群列表", "detail": "读取群详情", "pagination": "读取群列表分页", "storage": "保存群目录"}[e.stage]
	reason := map[string]string{
		"provider_timeout": "请求超时，请稍后重试", "provider_permission_denied": "企微权限不足，请检查客户联系权限",
		"provider_credentials_invalid": "企微授权已失效，请检查授权配置", "provider_rate_limited": "企微请求频率受限，请稍后重试",
		"provider_response_invalid": "企微返回数据不完整", "owner_mismatch": "群主信息已变化，请刷新后重试",
		"storage_unavailable": "目录保存失败，请稍后重试", "provider_unavailable": "企微服务暂不可用，请稍后重试",
	}[e.reason]
	return stage + "失败：" + reason
}
