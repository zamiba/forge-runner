// Package runner installs and launches the programs that run media a host
// cannot run itself: emulators, and compatibility layers such as Proton.
//
// "Runner" rather than "emulator" because Proton is neither an emulator nor a
// special case, and Lutris has used the word for exactly this for years.
//
// # What is here and what is not
//
// This package owns a runner's definition, finding its newest release,
// installing it through forge, and building the command line to launch something
// with it. It owns none of what follows from a game being up: no one-at-a-time
// slot, no profiles, no playtime, no events beyond the ones forge reports while
// installing. Those belong to whichever program is hosting — PortForge and a
// living-room client will answer them differently, and a package that decided
// for them would be useless to the second one.
//
// The division is deliberate and it is where the reuse lives: two hosts can
// disagree about what owning a running game means while agreeing exactly on
// which asset to download for an arm64 Mac.
package runner

// A runner is three files in one folder, each owned by a different layer:
//
//	.mediaitem.json   the MediaItem — what the program is (go-mediaitems' format)
//	.forge.json       how to build or unpack it (forge's format)
//	.runner.json      what to install and what to send it (this package's)
//
// Being a runner is not a kind of program, it is a job given to one, which is
// why .runner.json exists at all rather than those fields living in the
// MediaItem. It keeps every forge-shaped concern out of a schema that a
// MediaItem database will also carry, and it means a compatibility layer needs
// no new item type: it is a SoftwareApplication with a .runner.json beside it.
const (
	MetadataFileName   = ".mediaitem.json"
	SpecFileName       = ".forge.json"
	RunnerSpecFileName = ".runner.json"
)

// ItemType is the item type a runner's MediaItem carries by default, the only
// one this package ships definitions for.
//
//	SoftwareApplication   any program at all — schema.org's term, so a borrowing
//	└─ SoftwareEmulator   adds emulates, achievements
//
// A compatibility layer is a SoftwareApplication and needs no middle level,
// which is why Store reads whichever type folder a definition is filed under
// rather than assuming this one.
const (
	ItemType       = "SoftwareEmulator"
	ParentItemType = "SoftwareApplication"
)

// Runner is the pair as it exists on disk: the item, and the spec beside it.
type Runner struct {
	Item SoftwareEmulator `json:"item"`
	Spec RunnerSpec       `json:"spec"`
}

// ParentItemType is a MediaItem's declared parent type.
type ParentType struct {
	Title         string `json:"title"`
	SchemaVersion string `json:"schemaVersion"`
}

// SoftwareEmulator is the MediaItem for a program: what it is, independent of
// how anyone installs it.
type SoftwareEmulator struct {
	ItemType       string     `json:"_itemType"`
	ParentItemType ParentType `json:"_parentItemType"`
	ItemTitle      string     `json:"_itemTitle"`

	Title       string `json:"title"`
	Description string `json:"description"`
	// Publisher is who put the program out, and it is the field the _itemTitle
	// composes from — "Gopher64 · gopher64". For anything distributed from a Git
	// forge that is the account owning the repository, which is why it can
	// differ from Developer: loganmc10 writes Gopher64, the gopher64
	// organisation publishes it.
	//
	// Publisher rather than a year, which is what a VideoGame composes from,
	// because software on a rolling release has no meaningful first year and the
	// collision that actually happens is a fork — and a fork has a different
	// owner by definition.
	Publisher string `json:"publisher,omitempty"`
	Developer string `json:"developer,omitempty"`
	Homepage  string `json:"homepage,omitempty"`
	License   string `json:"license,omitempty"`

	// Emulates are the systems this program emulates, as the item types of what
	// it reads. A fact about the program wherever it came from, which is why it
	// is here and RunnerSpec.Runs is not.
	Emulates []string `json:"emulates,omitempty"`

	// Achievements records what the program can actually award, which is not the
	// same as whether it supports a service at all: Gopher64 has
	// RetroAchievements built in but is softcore-only, because RetroAchievements
	// grants hardcore on the frontend's behaviour rather than the emulator's.
	// Anyone choosing a runner for achievements needs that said out loud.
	Achievements *Achievements `json:"achievements,omitempty"`
}

// RunnerSpec is .runner.json: what this package needs in order to install a
// program and to know what to send it. Neither field describes the program,
// which is why neither is in the MediaItem.
type RunnerSpec struct {
	// Runs are the item types a host should dispatch to this runner.
	//
	// Deliberately not the same field as SoftwareEmulator.Emulates: Proton runs
	// Windows games and emulates nothing, so dispatch cannot be read off
	// emulation. For an emulator the two are usually equal, and a runner may
	// still run less than it emulates — a build shipped without a plugin, or a
	// system upstream calls experimental.
	Runs []string `json:"runs,omitempty"`

	// Dependencies are what must already be on the machine, which forge cannot
	// express: its own `dependencies` is a PATH check for commands and doubles
	// as its `run` allowlist, and a shared library is neither.
	Dependencies Dependencies `json:"dependencies,omitempty"`

	// ReleaseSource says where to look for the newest release, because forge's
	// fetch step takes a literal URL and has no notion of "latest". A spec
	// shipped inside a binary cannot hardcode one: that would make an emulator
	// update need a release of the host, and a URL that went dead could not be
	// fixed without one.
	ReleaseSource ReleaseSource `json:"releaseSource"`
}

// ReleaseSource is where a runner's newest release is looked up.
type ReleaseSource struct {
	Host string `json:"host"` // "github"
	Repo string `json:"repo"` // "owner/name"
}

// Achievements describes a program's achievement support.
type Achievements struct {
	Provider string `json:"provider"`
	Hardcore bool   `json:"hardcore"`
	Note     string `json:"note,omitempty"`
}

// Executable is something a finished install can run, as forge defined it.
type Executable struct {
	Path  string   `json:"path"`
	Title string   `json:"title"`
	Args  []string `json:"args,omitempty"`
}

// RunsItemType reports whether a host should dispatch an item type here.
func (r Runner) RunsItemType(itemType string) bool {
	for _, t := range r.Spec.Runs {
		if t == itemType {
			return true
		}
	}
	return false
}

// For returns the runners that can run an item type, in the order given.
func For(runners []Runner, itemType string) []Runner {
	var out []Runner
	for _, r := range runners {
		if r.RunsItemType(itemType) {
			out = append(out, r)
		}
	}
	return out
}
