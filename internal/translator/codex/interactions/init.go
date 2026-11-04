package interactions

import (
	. "github.com/265866/taproot/internal/constant"
	"github.com/265866/taproot/internal/interfaces"
	"github.com/265866/taproot/internal/translator/translator"
)

func init() {
	translator.Register(
		Interactions,
		Codex,
		ConvertInteractionsRequestToCodex,
		interfaces.TranslateResponse{
			Stream:    ConvertCodexResponseToInteractions,
			NonStream: ConvertCodexResponseToInteractionsNonStream,
		},
	)
}
