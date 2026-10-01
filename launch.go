package runner

import (
	"context"
	"fmt"

	"github.com/zamiba/forge/engine"
)

// LaunchOptions is what a launch resolves its references against.
type LaunchOptions struct {
	// MediaPath is the file being played, answering ${romPath}.
	MediaPath string
	// ProfilePath answers ${profilePath} for a runner whose spec takes the
	// profile as a flag. Empty when there is none, and a spec that asks for one
	// anyway fails here rather than at the moment the program starts.
	ProfilePath string
}

// LaunchArgs builds the argument list for running something with a runner.
//
// The media file is handed over as a fixed path rather than through any kind of
// library lookup: a runner has no declared dependencies to resolve against, and
// the file is the one the person just chose rather than whichever candidate
// happens to match. A host that wants matching does it before calling this.
func LaunchArgs(exe Executable, opts LaunchOptions) ([]string, error) {
	providers := map[string]engine.Provider{}
	if opts.MediaPath != "" {
		providers["rom"] = engine.FixedProvider{Default: opts.MediaPath}
	}
	if opts.ProfilePath != "" {
		providers["profile"] = engine.FixedProvider{Default: opts.ProfilePath}
	}

	forgeExe := engine.Executable{Path: exe.Path, Title: exe.Title, Args: exe.Args}
	args, err := forgeExe.LaunchArgs(context.Background(), providers)
	if err != nil {
		return nil, fmt.Errorf("cannot build the launch command: %w", err)
	}
	return args, nil
}
