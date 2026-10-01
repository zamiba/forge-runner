package runner

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/zamiba/forge/engine"
)

// builtinFS holds the runner definitions this package ships.
//
// The "all:" prefix is load-bearing: go:embed skips names beginning with a dot
// unless it is given, and every file in a MediaItem folder begins with one.
// Without it this embeds the directories and nothing inside them.
//
//go:embed all:runners
var builtinFS embed.FS

// builtinRoot is the directory inside builtinFS, and the name the definitions
// are written under on disk.
const builtinRoot = "runners"

// Store is a directory of runner definitions on disk.
type Store struct{ Dir string }

// Materialise writes the built-in definitions under root and returns a Store
// over them.
//
// To disk, rather than reading from the embedded FS, because every layer below
// this one takes a filesystem path — forge's spec loader, its step handlers, and
// a host's own config and state readers. Extracting once buys all of it instead
// of an fs.FS refactor of each.
//
// # Why the embedded folders are slugs and not item titles
//
// A MediaItem's folder name is its _itemTitle. That invariant cannot hold inside
// a Go binary, because go:embed refuses the standard's separator: embedded paths
// go through module.CheckFilePath, which allows Unicode letters and digits — so
// "Pokémon" and "日本" are fine — but only a small set of ASCII punctuation, and
// U+00B7 MIDDLE DOT is none of those. A folder named "Gopher64 · gopher64"
// makes the whole tree unembeddable, and the build error says only "contains no
// embeddable files", naming no file, folder or character.
//
// So the embedded tree is keyed by an ASCII slug and the real title is read from
// the item itself. The invariant holds where it means something: on disk, in the
// tree the loaders read. The embedded copy is a build artifact and no MediaItem
// store at all.
//
// The definitions belong to the binary, so they are overwritten every time: one
// edited by hand is not a customisation to preserve, it is a file that will
// disagree with the code reading it. Nothing else in the tree is touched, which
// is what lets a host keep its own state in the same folders.
func Materialise(root string) (Store, error) {
	s := Store{Dir: filepath.Join(root, builtinRoot)}

	typeDirs, err := fs.ReadDir(builtinFS, builtinRoot)
	if err != nil {
		return Store{}, fmt.Errorf("runners: %w", err)
	}
	for _, td := range typeDirs {
		if !td.IsDir() {
			continue
		}
		slugs, err := fs.ReadDir(builtinFS, path.Join(builtinRoot, td.Name()))
		if err != nil {
			return Store{}, fmt.Errorf("runners: %w", err)
		}
		for _, sd := range slugs {
			if !sd.IsDir() {
				continue
			}
			src := path.Join(builtinRoot, td.Name(), sd.Name())
			if err := materialiseOne(src, s.Dir, td.Name()); err != nil {
				return Store{}, fmt.Errorf("runners: %s: %w", src, err)
			}
		}
	}
	return s, nil
}

// materialiseOne copies one embedded item folder to its real item title.
func materialiseOne(src, destRoot, itemType string) error {
	data, err := builtinFS.ReadFile(path.Join(src, MetadataFileName))
	if err != nil {
		return err
	}
	var head struct {
		ItemType  string `json:"_itemType"`
		ItemTitle string `json:"_itemTitle"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return err
	}
	if head.ItemTitle == "" {
		return fmt.Errorf("%s has no _itemTitle, so it has no folder name", MetadataFileName)
	}
	// The embedded folder is a slug, so the item type is the only part of the
	// path that still carries meaning — and a definition filed under the wrong
	// one would be invisible to a lookup that goes by type.
	if head.ItemType != itemType {
		return fmt.Errorf("_itemType is %q but it is filed under %q", head.ItemType, itemType)
	}

	dest := filepath.Join(destRoot, head.ItemType, head.ItemTitle)
	return fs.WalkDir(builtinFS, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, src), "/")
		out := filepath.Join(dest, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(out, 0755)
		}
		b, err := builtinFS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0644)
	})
}

// List reads every definition in the store, sorted by title.
func (s Store) List() ([]Runner, error) {
	typeDirs, err := os.ReadDir(s.Dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var out []Runner
	for _, td := range typeDirs {
		if !td.IsDir() {
			continue
		}
		items, err := os.ReadDir(filepath.Join(s.Dir, td.Name()))
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if !it.IsDir() {
				continue
			}
			r, err := s.Get(td.Name(), it.Name())
			if err != nil {
				return nil, err
			}
			out = append(out, *r)
		}
	}
	sortRunners(out)
	return out, nil
}

// Get reads one definition: the MediaItem, and the runner spec beside it.
func (s Store) Get(itemType, itemTitle string) (*Runner, error) {
	base := filepath.Join(s.Dir, itemType, itemTitle)

	data, err := os.ReadFile(filepath.Join(base, MetadataFileName))
	if err != nil {
		return nil, err
	}
	var item SoftwareEmulator
	if err := json.Unmarshal(data, &item); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(itemTitle, MetadataFileName), err)
	}
	// The folder name is the identifier, so a definition whose _itemTitle
	// disagrees with it cannot be found again by the title it claims. Inside a
	// shipped binary that is a packaging mistake rather than something to read
	// leniently past.
	if item.ItemTitle != itemTitle {
		return nil, fmt.Errorf("%s: _itemTitle is %q but the folder is %q", itemTitle, item.ItemTitle, itemTitle)
	}

	// Required. A program's MediaItem without one is a perfectly good item that
	// simply is not a runner, and nothing should have come looking here for it.
	specData, err := os.ReadFile(filepath.Join(base, RunnerSpecFileName))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(itemTitle, RunnerSpecFileName), err)
	}
	var spec RunnerSpec
	if err := json.Unmarshal(specData, &spec); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(itemTitle, RunnerSpecFileName), err)
	}

	return &Runner{Item: item, Spec: spec}, nil
}

// Find reads one definition by item title alone, searching every type folder.
// It is the lookup a host wants when all it has is a title it stored earlier.
func (s Store) Find(itemTitle string) (*Runner, error) {
	typeDirs, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	for _, td := range typeDirs {
		if !td.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.Dir, td.Name(), itemTitle)); err == nil {
			return s.Get(td.Name(), itemTitle)
		}
	}
	return nil, fmt.Errorf("no runner called %q", itemTitle)
}

// Specs reads a runner's forge builds.
func (s Store) Specs(r Runner) ([]engine.Spec, error) {
	path := filepath.Join(s.Dir, r.Item.ItemType, r.Item.ItemTitle, SpecFileName)
	file, err := engine.LoadSpecFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", SpecFileName, err)
	}
	if file == nil {
		return nil, fmt.Errorf("%s: no %s", r.Item.ItemTitle, SpecFileName)
	}
	return file.Specs, nil
}

func sortRunners(rs []Runner) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && rs[j].Item.Title < rs[j-1].Item.Title; j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
}

// Executables returns the executables a runner's build declares for a platform,
// without installing anything.
//
// It reads them out of the spec's defineExecutable steps, interpolated with the
// same variables an install would bind. That makes it the same answer an install
// produces, from the same source, which is the point: a caller that keeps no
// record of what it installed — a command line, a script — can still find the
// program afterwards, and a caller that does keep one has something to check it
// against.
//
// The release tag is not known here and is not needed: it belongs to the fetch
// URL, not to an executable's path. A spec that put ${args.tag} in one would
// come back with the reference intact rather than silently wrong, which is
// Interpolate's deliberate behaviour for a name it has no value for.
func (s Store) Executables(r Runner, hostPlatform string) ([]Executable, error) {
	specs, err := s.Specs(r)
	if err != nil {
		return nil, err
	}
	target := SelectPlatform(specs, hostPlatform)
	spec := engine.Select(specs, target, "")
	if spec == nil {
		return nil, unsupported(r, hostPlatform, specs)
	}

	platformVars, versionVars := spec.VarsFor(target, "")
	vars := map[string]string{"platform": target}
	for k, v := range platformVars {
		vars["platform."+k] = v
	}
	for k, v := range versionVars {
		vars["version."+k] = v
	}

	var out []Executable
	for _, step := range spec.Steps {
		if step.Step != "defineExecutable" {
			continue
		}
		args := make([]string, len(step.Args))
		for i, a := range step.Args {
			args[i] = engine.Interpolate(a, vars)
		}
		out = append(out, Executable{
			Path:  engine.Interpolate(step.Executable, vars),
			Title: engine.Interpolate(step.Title, vars),
			Args:  args,
		})
	}
	return out, nil
}
