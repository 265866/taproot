package claude

import (
	"context"

	. "github.com/265866/taproot/internal/constant"
	"github.com/265866/taproot/internal/interfaces"
	"github.com/265866/taproot/internal/translator/translator"
	sdktranslator "github.com/265866/taproot/sdk/translator"
)

func init() {
	translator.Register(
		Claude,
		Codex,
		ConvertClaudeRequestToCodex,
		interfaces.TranslateResponse{
			Stream:     ConvertCodexResponseToClaude,
			NonStream:  ConvertCodexResponseToClaudeNonStream,
			TokenCount: ClaudeTokenCount,
		},
	)
	sdktranslator.RegisterRequestEnvelope(sdktranslator.FormatClaude, sdktranslator.FormatCodex,
		func(ctx context.Context, req sdktranslator.RequestEnvelope) sdktranslator.RequestEnvelope {
			req.Body, req.Err = ConvertClaudeRequestToCodexContext(ctx, req.Model, req.Body, req.Stream)
			return req
		})
}
