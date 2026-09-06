package kafkaconn

// oauth.go：OAUTHBEARER SASL（Phase 3，IMPL_PLAN §12.2.3）。
//   - 机制层用 franz-go pkg/sasl/oauth（authFn 每次新 SASL 会话调用，
//     msk_iam 天然获得新签名 token，static_token 恒返同一 token）；
//   - token 来源（oauth_token_source）：
//       msk_iam      = AWS MSK IAM 签名 token（aws-msk-iam-sasl-signer-go；
//                      凭据走 default 链，msk_access_key_id/msk_secret_access_key/
//                      msk_session_token 可选显式覆盖——照 §11.5 Glue
//                      auth_mode=default 范式）；
//       static_token = 静态 token 直供（oauth_static_token secret），
//                      Expiration=0 表示不过期（由 SASL 会话生命周期兜底）；
//   - OIDC token endpoint 交换仍非目标（§0.2）。
//
// 凭据红线（§6）：msk_secret_access_key/msk_session_token/oauth_static_token
// 走 secret binding，仅进签名/token 通道，不落日志/审计/事件/回显。

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-msk-iam-sasl-signer-go/signer"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/oauth"
)

// oauthTokenTimeout 单次 token 获取超时（无 IMDS/无凭据环境快速收敛，
// §12.7：错误串经 friendlyKafkaError 映射由 H 路同步）。
const oauthTokenTimeout = 10 * time.Second

// oauthTokenProvider 抽象 token 来源（单测覆盖 static / msk_iam 构造）。
type oauthTokenProvider interface {
	// Token 返回 bearer token 与过期毫秒（0 = 不过期/由会话生命周期兜底）。
	Token(ctx context.Context) (token string, expiryMs int64, err error)
}

// newOauthTokenProvider 按 Profile + connSecrets 构造 token provider
// （OauthTokenSource 已 NormalizeProfile 归一化；防御性再归一一次）。
func newOauthTokenProvider(profile Profile, secrets connSecrets) (oauthTokenProvider, error) {
	switch NormalizeOauthTokenSource(profile.OauthTokenSource) {
	case OauthTokenSourceStatic:
		return &staticOauthTokenProvider{token: secrets.OauthStaticToken}, nil
	case OauthTokenSourceMSKIAM:
		accessKeyID := strings.TrimSpace(profile.MSKAccessKeyID)
		secretAccessKey := strings.TrimSpace(secrets.MSKSecretAccessKey)
		if (accessKeyID == "") != (secretAccessKey == "") {
			return nil, errf("mskAccessKeyID and mskSecretAccessKey must be provided together to override the default AWS credential chain")
		}
		return &mskIAMTokenProvider{
			region:          strings.TrimSpace(profile.MSKRegion),
			accessKeyID:     accessKeyID,
			secretAccessKey: secretAccessKey,
			sessionToken:    strings.TrimSpace(secrets.MSKSessionToken),
		}, nil
	default:
		return nil, errf("oauthTokenSource must be msk_iam or static_token")
	}
}

// mskIAMTokenProvider 用 MSK IAM signer 生成 SASL token（每次调用新签名）。
type mskIAMTokenProvider struct {
	region string
	// 可选显式凭据（default 链覆盖；空 = default 链）。
	accessKeyID     string
	secretAccessKey string
	sessionToken    string
}

// Token 生成 MSK IAM 签名 token。显式 AK/SK 时走
// GenerateAuthTokenFromCredentialsProvider（static provider），否则
// GenerateAuthToken（signer 内部 default 凭据链，照 Glue auth_mode=default）。
func (p *mskIAMTokenProvider) Token(ctx context.Context) (string, int64, error) {
	region := strings.TrimSpace(p.region)
	if region == "" {
		return "", 0, errf("mskRegion is required for OAUTHBEARER token_source=msk_iam")
	}
	if p.accessKeyID != "" && p.secretAccessKey != "" {
		return signer.GenerateAuthTokenFromCredentialsProvider(ctx, region,
			awscredentials.NewStaticCredentialsProvider(p.accessKeyID, p.secretAccessKey, p.sessionToken))
	}
	return signer.GenerateAuthToken(ctx, region)
}

// staticOauthTokenProvider 直供静态 token（Expiration=0 表示不过期）。
type staticOauthTokenProvider struct{ token string }

func (p *staticOauthTokenProvider) Token(_ context.Context) (string, int64, error) {
	if strings.TrimSpace(p.token) == "" {
		return "", 0, errf("oauthStaticToken is required when oauthTokenSource is \"static_token\"")
	}
	return p.token, 0, nil
}

// buildOauthSASLOpt 构建 franz-go OAUTHBEARER 机制（构造期不触网：token
// 获取延迟到每次 SASL 会话；kgo 拨号时按会话调用 authFn）。
func buildOauthSASLOpt(profile Profile, secrets connSecrets) (kgo.Opt, error) {
	provider, err := newOauthTokenProvider(profile, secrets)
	if err != nil {
		return nil, err
	}
	mechanism := oauth.Oauth(func(ctx context.Context) (oauth.Auth, error) {
		ctx, cancel := context.WithTimeout(ctx, oauthTokenTimeout)
		defer cancel()
		token, _, err := provider.Token(ctx)
		if err != nil {
			return oauth.Auth{}, err
		}
		return oauth.Auth{Token: token}, nil
	})
	return kgo.SASL(mechanism), nil
}
