package service

import (
	"context"
	"fmt"

	"repotory/internal/service/wikigen"
)

// wikigenChat is the service-side end of the wikigen.Chat seam (ADR-0060 step
// three). It is the whole reason the model-driven write side could leave the
// monolith: what stays behind is endpoint resolution, the API key and the
// ADR-0030 audit row — the things that must not travel — and what crosses is one
// narrow interface. The kernels never see aiChatConfig, chatMessage or
// *AITestResult; they see Role/Content in and Text/FinishReason out.
type wikigenChat struct{ s *Service }

// Available reports whether an endpoint is configured at all. Any configuration
// failure becomes ErrNotConfigured, matching the pre-split behaviour where any
// aiChatConfig error produced the kernel's "AI 端点未配置" note and no call —
// a run that never reached a model must not read as a run whose calls failed.
func (b wikigenChat) Available(ctx context.Context) error {
	if _, err := b.s.aiChatConfig(); err != nil {
		return fmt.Errorf("%w: %v", wikigen.ErrNotConfigured, err)
	}
	return nil
}

// Chat resolves the config, narrows the messages to the transport type and
// routes through the audited entry point. The batch ceiling is wikigen's
// BatchTimeout, applied here, so the kernels carry no timeout policy.
func (b wikigenChat) Chat(ctx context.Context, msgs []wikigen.Message) (wikigen.Reply, error) {
	cfg, err := b.s.aiChatConfig()
	if err != nil {
		return wikigen.Reply{}, fmt.Errorf("%w: %v", wikigen.ErrNotConfigured, err)
	}
	converted := make([]chatMessage, len(msgs))
	for i, m := range msgs {
		converted[i] = chatMessage{Role: m.Role, Content: m.Content}
	}
	res, err := b.s.chatWithAudit(cfg, converted, wikigen.BatchTimeout)
	if err != nil {
		return wikigen.Reply{}, err
	}
	return wikigen.Reply{Text: res.Reply, FinishReason: res.FinishReason}, nil
}
