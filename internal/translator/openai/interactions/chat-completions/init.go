package chat_completions

import (
	. "github.com/265866/taproot/internal/constant"
	"github.com/265866/taproot/internal/interfaces"
	"github.com/265866/taproot/internal/translator/translator"
)

func init() {
	translator.Register(
		OpenAI,
		Interactions,
		ConvertOpenAIRequestToInteractions,
		interfaces.TranslateResponse{
			Stream:    ConvertInteractionsResponseToOpenAI,
			NonStream: ConvertInteractionsResponseToOpenAINonStream,
		},
	)
	translator.Register(
		Interactions,
		OpenAI,
		ConvertInteractionsRequestToOpenAI,
		interfaces.TranslateResponse{
			Stream:    ConvertOpenAIResponseToInteractions,
			NonStream: ConvertOpenAIResponseToInteractionsNonStream,
		},
	)
}
