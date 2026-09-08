package imageproc

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"image/gif"
	"testing"
)

//go:embed testdata/legacy-animation.json
var legacyAnimationJSON []byte

type animationMetadata struct {
	Format       string `json:"format"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Pages        int    `json:"pages"`
	PageHeight   int    `json:"pageHeight"`
	Delay        []int  `json:"delay"`
	Loop         int    `json:"loop"`
	GIFLoopCount int    `json:"gifLoopCount"`
}

type animationContract struct {
	Source      []byte            `json:"sourceBase64"`
	Input       animationMetadata `json:"input"`
	Transformed animationMetadata `json:"transformed"`
}

func loadAnimationContract(t *testing.T) animationContract {
	t.Helper()
	var contract animationContract
	if err := json.Unmarshal(legacyAnimationJSON, &contract); err != nil {
		t.Fatalf("decode legacy animation contract: %v", err)
	}
	return contract
}

func TestLegacyAnimationFixture(t *testing.T) {
	contract := loadAnimationContract(t)
	decoded, err := gif.DecodeAll(bytes.NewReader(contract.Source))
	if err != nil {
		t.Fatalf("decode animation fixture: %v", err)
	}
	if len(decoded.Image) != contract.Input.Pages || decoded.Config.Width != contract.Input.Width || decoded.Config.Height != contract.Input.PageHeight {
		t.Fatalf("fixture = %dx%d with %d pages, want %dx%d with %d pages", decoded.Config.Width, decoded.Config.Height, len(decoded.Image), contract.Input.Width, contract.Input.PageHeight, contract.Input.Pages)
	}
	delayMilliseconds := make([]int, len(decoded.Delay))
	for index, centiseconds := range decoded.Delay {
		delayMilliseconds[index] = centiseconds * 10
	}
	if !equalInts(delayMilliseconds, contract.Input.Delay) || decoded.LoopCount != contract.Input.GIFLoopCount {
		t.Fatalf("fixture delay/loop = %v ms/%d, want %v ms/%d", delayMilliseconds, decoded.LoopCount, contract.Input.Delay, contract.Input.GIFLoopCount)
	}
}

func equalInts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
