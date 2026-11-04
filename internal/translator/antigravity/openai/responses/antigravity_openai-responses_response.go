package responses

import (
	"context"

	. "github.com/265866/taproot/internal/translator/gemini/openai/responses"
	"github.com/MoeForge/moego/ojson"
)

func ConvertAntigravityResponseToOpenAIResponses(ctx context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) [][]byte {
	responseResult := ojson.GetBytesNoCopy(rawJSON, "response")
	if responseResult.Exists() {
		rawJSON = []byte(responseResult.Raw)
	}
	return ConvertGeminiResponseToOpenAIResponses(ctx, modelName, originalRequestRawJSON, requestRawJSON, rawJSON, param)
}

func ConvertAntigravityResponseToOpenAIResponsesNonStream(ctx context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) []byte {
	responseResult := ojson.GetBytesNoCopy(rawJSON, "response")
	if responseResult.Exists() {
		rawJSON = []byte(responseResult.Raw)
	}

	requestResult := ojson.GetBytesNoCopy(originalRequestRawJSON, "request")
	if requestResult.Exists() {
		originalRequestRawJSON = []byte(requestResult.Raw)
	}

	requestResult = ojson.GetBytesNoCopy(requestRawJSON, "request")
	if requestResult.Exists() {
		requestRawJSON = []byte(requestResult.Raw)
	}

	return ConvertGeminiResponseToOpenAIResponsesNonStream(ctx, modelName, originalRequestRawJSON, requestRawJSON, rawJSON, param)
}
