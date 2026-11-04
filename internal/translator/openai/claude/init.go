package claude

import (
	. "github.com/265866/taproot/internal/constant"
	"github.com/265866/taproot/internal/interfaces"
	"github.com/265866/taproot/internal/translator/translator"
)

func init() {
	translator.Register(
		Claude,
		OpenAI,
		ConvertClaudeRequestToOpenAI,
		interfaces.TranslateResponse{
			Stream:     ConvertOpenAIResponseToClaude,
			NonStream:  ConvertOpenAIResponseToClaudeNonStream,
			TokenCount: ClaudeTokenCount,
		},
	)
}
