package cli

import (
	"fmt"

	"github.com/inhere/suga/internal/chat"
	"github.com/inhere/suga/internal/output/termtext"
	"github.com/inhere/suga/internal/wiring"
	agentcore "github.com/inhere/suga/pkg/agentcore"
)

// maxRunImages mirrors chat's pending-image cap (chat maxPendingImages): one
// run's --image list never carries more pictures than one message can attach
// anyway (the per-request caps of the store apply below this bound).
const maxRunImages = 16

// imageReader loads one picture with the chat :image rules (wiring.ReadImage:
// view_image path rules, input limits and scaling).
type imageReader func(path string) (agentcore.ImagePart, string, error)

// visionGate reports whether the current model accepts images (nil: yes).
type visionGate interface{ CanSee() bool }

// prepareRunImages turns the run's --image paths into the image parts that ride
// with Prompt on the chat :image pipeline. Relative paths resolve against the
// run's workspace; a failure (model of the run cannot see, list too long, one
// unreadable picture) fails the run before its first turn.
func prepareRunImages(rt *wiring.Runtime, paths []string) ([]agentcore.ImagePart, error) {
	modelRef := ""
	if rt.Runner != nil {
		modelRef = chat.ModelLabel(rt.Runner.ProviderName(), rt.Runner.Model())
	}
	return loadRunImages(rt.ReadImage, rt.VisionGate(), paths, modelRef)
}

// loadRunImages is the tested core of prepareRunImages. The model ref only
// names the model in the "cannot see" error; the reader failures already name
// the path.
func loadRunImages(read imageReader, gate visionGate, paths []string, modelRef string) ([]agentcore.ImagePart, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	// The vision check comes first, so a run for a non-vision model fails at
	// once instead of reading pictures it would have to drop (run must not
	// silently discard an image): the CLI carries no :model switch, the caller
	// picks the model with --model or the config's vision setting.
	if gate != nil && !gate.CanSee() {
		return nil, fmt.Errorf("当前模型 %s 不支持看图，无法使用 --image 附图；请用 --model 指定支持看图的模型，或在配置中为该模型开启 vision",
			termtext.Clean(modelRef))
	}
	if len(paths) > maxRunImages {
		return nil, fmt.Errorf("--image 最多 %d 张，收到 %d 张", maxRunImages, len(paths))
	}
	images := make([]agentcore.ImagePart, 0, len(paths))
	for i, path := range paths {
		part, _, err := read(path)
		if err != nil {
			// One unreadable picture fails the whole run: the model must never
			// answer without knowing a picture was meant to be there.
			return nil, fmt.Errorf("读取 --image 第 %d 个（%s）失败：%v", i+1, path, err)
		}
		images = append(images, part)
	}
	return images, nil
}
