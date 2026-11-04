package chat_completions

import (
	. "github.com/265866/taproot/internal/constant"
	"github.com/265866/taproot/internal/interfaces"
	"github.com/265866/taproot/internal/translator/translator"
)

func init() {
	translator.Register(
		OpenAI,
		Antigravity,
		ConvertOpenAIRequestToAntigravity,
		interfaces.TranslateResponse{
			Stream:    ConvertAntigravityResponseToOpenAI,
			NonStream: ConvertAntigravityResponseToOpenAINonStream,
		},
	)
}
