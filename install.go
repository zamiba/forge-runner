package runner

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/zamiba/forge/engine"
)

// TagArg is the install argument carrying the release tag. A runner's spec
// interpolates it into its fetch URL as ${args.tag}, and the value comes from
// the release feed rather than from a person.
//
// This is how a spec shipped inside a binary avoids hardcoding a URL. The spec
// declares no versions at all, which is the truthful account — it does not know
// them, the feed does — and the resolved tag is given to the engine as both the
// argument and the build's version, so a host can record what it actually
// installed.
const TagArg = "tag"

// InstallOptions is what a host supplies for an install. Everything here is the
// host's to decide: where the program goes, what the machine is, and where
// progress and logs are sent.
type InstallOptions struct {
	// Dir is the install directory. It is created if it does not exist.
	Dir string
	// Platform is the host platform as the specs spell it — "Linux-x64",
	// "Mac-arm64". The exact form matters: a spec may publish separate builds
	// per architecture, and an asset table keyed by one name is not reachable by
	// another.
	Platform string
	// Tag pins a release. Empty resolves the newest, which is the ordinary case.
	Tag string

	// Events, Log and HTTPClient are passed to forge untouched. A host that
	// wants a progress bar supplies Events; one that does not, does not.
	Events     func(engine.Event)
	Log        io.Writer
	HTTPClient *http.Client
}

// Installed is what an install produced.
type Installed struct {
	Tag         string
	Platform    string
	Executables []Executable
}

// Install fetches and installs a runner, resolving its newest release first
// unless opts pins one.
//
// It does not record anything. What was installed is returned, and writing that
// down is the host's — a host already keeps install state in its own shape, and
// a second record here would be the one that goes stale.
func (s Store) Install(ctx context.Context, r Runner, opts InstallOptions) (*Installed, error) {
	if opts.Dir == "" {
		return nil, fmt.Errorf("no install directory")
	}
	specs, err := s.Specs(r)
	if err != nil {
		return nil, err
	}

	tag := opts.Tag
	if tag == "" {
		if tag, err = LatestTag(r.Spec.ReleaseSource); err != nil {
			return nil, fmt.Errorf("%s: could not find the newest release: %w", r.Item.Title, err)
		}
	}

	target := SelectPlatform(specs, opts.Platform)
	spec := engine.Select(specs, target, "")
	if spec == nil {
		return nil, unsupported(r, opts.Platform, specs)
	}

	if err := os.MkdirAll(opts.Dir, 0755); err != nil {
		return nil, err
	}

	args := map[string]string{TagArg: tag}
	eopts := spec.BuildOptions(target, tag, args, []string{tag})
	eopts.RootDir = opts.Dir
	eopts.RequireExecutable = true
	eopts.Events = opts.Events
	eopts.Log = opts.Log
	eopts.HTTPClient = opts.HTTPClient
	// No providers: a runner has no content dependencies of its own, and the
	// ${romPath} in its defineExecutable is resolved at launch against whatever
	// is being played rather than at install time.

	res, err := engine.Run(ctx, eopts)
	if err != nil {
		return nil, err
	}

	out := &Installed{Tag: tag, Platform: target}
	for _, e := range res.Executables {
		out.Executables = append(out.Executables, Executable{Path: e.Path, Title: e.Title, Args: e.Args})
	}
	return out, nil
}

// Supports reports whether any build covers a host platform. It is what a list
// consults before offering an Install button: a program published for three
// desktop platforms is not published for every desktop machine, and an
// architecture nobody built for has to say so rather than fail on click.
func (s Store) Supports(r Runner, hostPlatform string) bool {
	specs, err := s.Specs(r)
	if err != nil {
		return false
	}
	return engine.Select(specs, SelectPlatform(specs, hostPlatform), "") != nil
}

// unsupported says what the machine is and what the program is published for.
//
// Naming the host and not the resolved target, which is what the error used to
// do: SelectPlatform falls back to the bare OS name, so a program publishing
// Mac-arm64 and nothing else told an Intel Mac "no build for Mac" — which reads
// as there being no Mac build at all, when the truth is that there is one and it
// is for the other architecture. Listing what exists is also the only form of
// this message somebody can act on.
func unsupported(r Runner, host string, specs []engine.Spec) error {
	var published []string
	for i := range specs {
		published = append(published, specs[i].TargetPlatforms...)
	}
	if len(published) == 0 {
		return fmt.Errorf("%s has no builds at all", r.Item.Title)
	}
	return fmt.Errorf("%s has no build for %s; published for %s",
		r.Item.Title, host, strings.Join(published, ", "))
}

// SelectPlatform picks the name a spec spells the host platform with: an exact
// architecture match wins over the bare OS name, so a program shipping separate
// arm64 and x64 builds gets the right one, while one declaring only "Mac"
// still matches any Mac.
//
// When nothing matches, the bare OS name comes back, so a caller reports "no
// build for Mac" rather than "no build for Mac-x64" — and a spec declaring no
// platforms at all, which targets everything, is still selected.
func SelectPlatform(specs []engine.Spec, host string) string {
	base := host
	for i, c := range host {
		if c == '-' {
			base = host[:i]
			break
		}
	}
	fallback := ""
	for i := range specs {
		for _, p := range specs[i].TargetPlatforms {
			if p == host {
				return p
			}
			if p == base && fallback == "" {
				fallback = p
			}
		}
	}
	if fallback != "" {
		return fallback
	}
	return base
}
