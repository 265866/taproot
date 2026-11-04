package responses

import (
	. "github.com/265866/taproot/internal/constant"
	"github.com/265866/taproot/internal/interfaces"
	"github.com/265866/taproot/internal/translator/translator"
)

func init() {
	translator.Register(
		OpenaiResponse,
		Interactions,
		ConvertOpenAIResponsesRequestToInteractions,
		interfaces.TranslateResponse{
			Stream:    ConvertInteractionsResponseToOpenAIResponses,
			NonStream: ConvertInteractionsResponseToOpenAIResponsesNonStream,
		},
	)
	translator.Register(
		Interactions,
		OpenaiResponse,
		ConvertInteractionsRequestToOpenAIResponses,
		interfaces.TranslateResponse{
			Stream:    ConvertOpenAIResponsesResponseToInteractions,
			NonStream: ConvertOpenAIResponsesResponseToInteractionsNonStream,
		},
	)
}
