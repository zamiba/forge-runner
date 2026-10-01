# Changelog

## v0.2.0 - 2026-10-01

### Added

- **`dependencies` in `.runner.json`, for what must already be on the machine.**
  An object keyed by OS name — `Linux`, `Windows`, `Mac` — each with `libraries`
  (sonames, as the loader looks for them) and `hint` (what to tell a person,
  because a soname is not installable and the package name differs per
  distribution). By OS and not by platform, since a shared library's name does
  not change with the architecture even though the file does. This is
  deliberately not forge's `dependencies`, which is a PATH check for commands
  and doubles as its `run` allowlist: a shared library is neither a command nor
  something a build invokes, so forge has no way to express one and the failure
  surfaces as the program's own loader error after a successful install.

- **`MissingLibraries`, which answers before anything is downloaded.** It
  searches `ldconfig -p` and, where there is no such cache, the directories a
  loader searches by default plus `LD_LIBRARY_PATH`. That is the one thing a
  declaration uniquely buys: telling somebody they need fontconfig is worth
  more before a 70MB download than once the program refuses to start. It
  returns nothing on Windows or macOS rather than guessing, because DLLs resolve
  beside the executable and frameworks live inside the bundle, so a search there
  would report absences that are not absences.

- **`VerifyLinked`, which asks the dynamic loader about an installed binary.**
  It runs `ldd` and reports what `ldd` says, because a declared list of names
  cannot express the failure that actually bites: Gopher64 needs `GLIBC_2.39`
  and `GLIBCXX_3.4.31`, and on an older distribution `libc.so.6` and
  `libstdc++.so.6` are both present, a name search says everything is fine, and
  the program still will not start. Only the loader knows, because only the
  loader checks symbol versions. It needs the binary, so it can only answer
  after an install — the two checks are complements and not alternatives.
  Nothing is reported where the check cannot run, on a non-Linux host or with no
  `ldd` present: a check that cannot run must not invent a pass or a failure,
  and the program's own error is then the fallback, which is where this was
  before.

### Changed

- **`forge-runner list` says what a runner is missing, and `launch` refuses
  before starting anything.** The refusal carries the loader's own complaints
  and the declared hint, because a host that starts programs detached sends that
  error to a stderr nobody reads — a launch failing this way otherwise looks
  like a launch that did nothing at all.

### Notes

- **A missing library is a warning and not a reason to refuse an install.** The
  download and unpacking succeed; only starting the program fails. Blocking the
  install would make a runner uninstallable on a machine where installing it
  first and adding the library second works perfectly.

- **Declare what somebody might plausibly lack, not everything the binary
  links.** `libc`, `libm`, `libstdc++` and `libgcc_s` are on every glibc
  desktop, so listing them adds noise and invents failures on a musl system
  where there is no `libc.so.6` to find and the program may still run. The
  binary already carries its full `DT_NEEDED` list and the loader already reads
  it; a declaration is for the ones that are genuinely missing sometimes.

## v0.1.0 - 2026-10-01

First release, extracted from PortForge so that more than one host can install
and launch the same runners from the same definitions.

### Added

- **A runner is three files in one folder**, each owned by a different layer:
  `.mediaitem.json` (the MediaItem — what the program is), `.forge.json` (how to
  build or unpack it) and `.runner.json` (what to install, and what to send it).
  Being a runner is not a kind of program, it is a job given to one, which is
  why `.runner.json` exists rather than those fields living in the MediaItem. It
  keeps every forge-shaped concern out of a schema a MediaItem database will
  also carry, and it means a compatibility layer such as Proton needs no new
  item type: it is a `SoftwareApplication` with a `.runner.json` beside it.

- **`Materialise`, `Store.List`, `Get`, `Find` and `Specs`**, over definitions
  embedded in the binary and written to disk once. To disk rather than read from
  the embedded FS because every layer below — forge's spec loader, its step
  handlers, a host's own state readers — takes a filesystem path, and extracting
  once buys all of it instead of an `fs.FS` refactor of each.

- **`LatestTag`, and a tag resolved at install time.** forge's `fetch` takes a
  literal URL and has no notion of "latest", deliberately — so a definition
  shipped inside a binary cannot hardcode one, because that would make an
  emulator update need a release of the host and a dead link could not be fixed
  without one. `.runner.json` says where to look, the spec writes `${args.tag}`
  into its fetch URL, and `Install` resolves the newest non-prerelease tag and
  passes it. A spec declares no versions at all, which is the truthful account:
  it does not know them, the feed does.

- **`Install`, which installs and reports but records nothing.** What was
  installed comes back and writing it down is the host's, because a host already
  keeps install state in its own shape and a second record here would be the one
  that goes stale. `Events`, `Log` and `HTTPClient` are the host's to supply and
  are passed to forge untouched.

- **`Executables`, which reads the declared executables out of a spec without
  installing.** Interpolated with the same variables an install would bind, so
  it is the same answer from the same source — which lets a caller that kept no
  record find the program afterwards, and gives one that did something to check
  against.

- **`LaunchArgs`**, which hands the media file over as a fixed path. A runner has
  no declared dependencies to resolve against, and the file is the one somebody
  just chose rather than whichever candidate happens to match; a host that wants
  matching does it before calling.

- **`SelectPlatform` and `Supports`.** An exact architecture match wins over the
  bare OS name, so a program shipping separate arm64 and x64 builds gets the
  right one while one declaring only `Mac` still matches any Mac. `Supports` is
  what a list consults before offering an Install button: a program published
  for three desktop platforms is not published for every desktop machine, and
  Gopher64 publishes no macOS x86_64 asset at all.

- **A command-line face**, `forge-runner list | latest | install | launch`. It
  exists partly so that anything can use this and not only a program that links
  it, and partly because a great deal of what a host does is hard to test when
  testing it needs a desktop session on particular hardware — checking that an
  arm64 build unpacks is a spare machine and a GUI session without this, and one
  ssh command with it.

- **Gopher64 as the first runner.** Nintendo 64, five assets across Linux,
  Windows and macOS, and softcore-only RetroAchievements — which the definition
  states, because supporting a service and being allowed to award its hardcore
  unlocks are two different claims and the second is the one worth knowing
  before a long game.

### Notes

- **`go:embed` cannot carry an `_itemTitle`.** Embedded paths go through
  `module.CheckFilePath`, which allows Unicode letters and digits — `Pokémon`
  and `日本` are fine — but only a small set of ASCII punctuation, and U+00B7
  MIDDLE DOT is not in it. A folder named `Gopher64 · gopher64` makes the whole
  tree unembeddable and the build says only `contains no embeddable files`,
  naming no file, folder or character. So the embedded tree is keyed by an ASCII
  slug and `Materialise` reads the real title out of the item. The
  folder-name-equals-identifier invariant holds on disk, where the loaders read;
  the embedded copy is a build artifact and no MediaItem store at all.

- **An unsupported machine is not an unsupported OS.** `SelectPlatform` falls
  back to the bare OS name, so an error built from it told an Intel Mac "no
  build for Mac" when a `Mac-arm64` build existed. Errors name the host and list
  what is published instead.
