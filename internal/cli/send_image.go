package cli

import (
	"fmt"
	"os"

	appconfig "github.com/inhere/suga/internal/config"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/internal/wiring"
)

// maxSendImages caps `suga send --image`, the same bound one message's pending
// pictures have in chat (chat maxPendingImages / run maxRunImages).
const maxSendImages = 16

// sendImageRefs prepares the pictures of one `suga send --image` request. A
// target whose process record does not declare FeatureImages is refused before
// anything is sent (an older receiver would take the text and silently drop the
// pictures, design send --image D2/D3); the pictures then go into the target
// session's images directory below $SUGA_HOME/sessions/<sid>/images.
func sendImageRefs(record personalprocess.Record, paths []string) ([]personalprocess.ImageRef, error) {
	tr := cliTranslator()
	if len(paths) == 0 {
		return nil, nil
	}
	if !record.HasFeature(personalprocess.FeatureImages) {
		return nil, processExitError{code: 5, err: fmt.Errorf("%s", cliText(tr, "cli.send.image_unsupported_short", map[string]any{"session": record.SessionID, "pid": record.PID}))}
	}
	sessionsDir, err := appconfig.ResolveSessionsDir()
	if err != nil {
		return nil, err
	}
	// A relative --image path is the sender's own: `suga send` runs in the
	// directory the person typed the path in, not in the target session's.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	refs, err := prepareSendImages(paths, cwd, sessionsDir, record.SessionID)
	if err != nil {
		return nil, processExitError{code: 1, err: err}
	}
	return refs, nil
}

// prepareSendImages turns the --image paths of `suga send` into the references
// the request carries (design send --image D1). Every picture is read, checked
// and scaled with the run --image rules (wiring.ReadImageAt: relative paths
// resolve against the sender's working directory, cwd) and then written into the
// target session's images directory with the storage layer's content-addressed
// atomic write, so a picture the session already holds is not written again.
// Any failure explains that nothing was delivered: a half-delivered message
// (text without its picture) is worse than no message.
func prepareSendImages(paths []string, cwd, sessionsDir, sessionID string) ([]personalprocess.ImageRef, error) {
	tr := cliTranslator()
	if len(paths) == 0 {
		return nil, nil
	}
	if len(paths) > maxSendImages {
		return nil, fmt.Errorf("%s", cliText(tr, "cli.image.too_many", map[string]any{"max": maxSendImages, "count": len(paths)}))
	}
	imagesDir := store.SessionImagesDir(sessionsDir, sessionID)
	refs := make([]personalprocess.ImageRef, 0, len(paths))
	for i, path := range paths {
		part, _, err := wiring.ReadImageAt(cwd, path)
		if err != nil {
			return nil, fmt.Errorf("%s", cliText(tr, "cli.image.read_failed", map[string]any{"index": i + 1, "path": path, "error": err}))
		}
		stored, err := store.StoreImage(imagesDir, part.Data, part.MediaType)
		if err != nil {
			return nil, fmt.Errorf("%s", cliText(tr, "cli.image.write_failed", map[string]any{"error": err}))
		}
		refs = append(refs, personalprocess.ImageRef{
			Ref: stored.Ref, SHA256: stored.SHA256, MediaType: stored.MediaType,
			Width: part.Width, Height: part.Height,
		})
	}
	return refs, nil
}
