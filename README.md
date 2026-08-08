# tuido

`gofmt` for todo lists, plus quick capture.

Markdown files are the sole source of truth. Your editor owns editing; tuido owns
capture, completion, formatting and sync. If tuido is deleted, nothing is lost —
there is no database, no index, no cache of your tasks.

```console
$ tuido add rotate vault certs -p high -d friday
✓ rotate vault certs → work/inbox

$ tuido ls
work/oncall
  ▲  Fix ALB drain timeout                              08-09   2d
  △  Rotate vault certs                                         6d
  •  Chase SRE on cutover                                      16d

  3 hidden: 1 blocked, 1 not started, 1 scheduled
  → tuido ls --all

$ tuido done drain
✓ done: Fix ALB drain timeout → work/oncall
```

## What `sort` does

The command worth seeing before you install anything. It reorders task lines —
open before done, unblocked before blocked, then priority, then due date — and
changes nothing else about the file.

**A file with no headings is one block**, so the whole list is ordered:

```markdown
- [ ] Chase SRE on cutover
- [x] Draft the runbook ✅ 2026-08-05
- [ ] Fix ALB drain timeout ⏫ 📅 2026-08-09
- [ ] Rotate vault certs 🔼 📅 2026-08-14
```

```console
$ tuido sort oncall
✓ sorted work/oncall (4 moved, by prio)
```

```markdown
- [ ] Fix ALB drain timeout ⏫ 📅 2026-08-09
- [ ] Rotate vault certs 🔼 📅 2026-08-14
- [ ] Chase SRE on cutover
- [x] Draft the runbook ✅ 2026-08-05
```

**Add headings and it sorts within them.** Headings, prose, blank lines and code
fences are fences: nothing crosses one, so a task never leaves the section you
filed it under. The whole diff of a sort on a structured note is the moves
themselves —

```diff
  # Oncall

  Rotation ends Friday.

- - [ ] Chase SRE on cutover
  - [ ] Fix ALB drain timeout ⏫ 📅 2026-08-09
+ - [ ] Chase SRE on cutover
  - [x] Draft the runbook ✅ 2026-08-05

  ## Later

- - [ ] Write the postmortem
  - [ ] Rotate vault certs 🔼 📅 2026-08-14
+ - [ ] Write the postmortem
```

— and running it again produces no diff at all:

```console
$ tuido sort oncall
· already sorted
```

That idempotence is what makes it safe to bind to save-on-write or a git hook:
your history records the tasks you changed, not the formatting.

`--by due` or `--by created` picks a different key for one run; a file can fix
its own with a `<!-- tuido: sort=due -->` marker, or opt out entirely with
`sort=none`.

## Install

```sh
go install github.com/ramtinJ95/tuido/cmd/tuido@latest
```

Or from a clone, which also installs the zsh completion:

```sh
git clone https://github.com/ramtinJ95/tuido
cd tuido && make install      # → ~/.local/bin/tuido
```

Then set it up. Every prompt has a flag equivalent, so a dotfiles bootstrap can
run it non-interactively:

```console
$ tuido init
todo root  [~/todo]: ~/notes/todo
  initialize a git repo there? [Y/n] y
  git remote (blank to skip): git@github.com:you/todo.git
  keep tuido's automatic commits off your GitHub contribution graph? [Y/n] y
  commits will be authored as tuido@localhost (repo-local)
first workspace  [work]: work

✓ wrote ~/.config/tuido/config.toml
✓ context set to `work`
```

The contribution-graph question exists because tuido commits on every edit, and
GitHub counts a commit toward your profile only when its author email is linked
to your account. Saying yes sets a repo-local `user.email` GitHub does not
know, so thousands of autosave commits stay off the graph — your global git
identity and every other repo are untouched. The setting lives in
`.git/config`, which never syncs, so answer it on each machine. To opt an
existing repo in:

```sh
git -C ~/notes/todo config user.email tuido@localhost
```

On a second machine, point `--remote` at the repo you already have. It clones
rather than initialising an empty one beside it:

```sh
tuido init --root ~/notes/todo --remote git@github.com:you/todo.git
```

[fzf](https://github.com/junegunn/fzf) is optional. With it on `PATH`, an
ambiguous query opens a picker; without it, tuido prints the candidates and
exits 1, so every command stays scriptable.

## Updating

```sh
tuido upgrade          # download and install the latest release
tuido upgrade --check  # just report whether one exists
```

tuido notices new versions on its own, at most once a day, in a detached
background process. It never checks *during* a command — the answer is read
from a cache file, so a slow or unreachable GitHub can't make `tuido ls` hang.
When there's something newer you get one line above your normal output:

```
⚠ tuido v0.3.1 is available (you have v0.3.0) — run `tuido upgrade`
```

Nothing is ever downloaded or replaced without you asking. `upgrade` fetches
the binary for your platform, checks it against the release's published sha256
manifest, and refuses to install if that doesn't match. The replacement is
atomic and keeps the existing file permissions.

To turn the reminder off entirely:

```toml
[update]
check = false
```

`go install github.com/ramtinJ95/tuido/cmd/tuido@latest` still works and picks
up the newest tag.

## Commands

| | |
|---|---|
| `tuido add [flags] <text…>` | capture a task (`-p` prio, `-d` due, `-t` tag, `-l` list) |
| `tuido done <fuzzy…>` | mark a task done |
| `tuido ls [list] [--all]` | show actionable tasks |
| `tuido sort [list] [--by …]` | reorder tasks within their blocks |
| `tuido fmt [list] \| fmt -` | expand `:p2` / `:due monday` shorthand into fields |
| `tuido open [query] [--root]` | open a list, or the whole repo, in `$EDITOR` |
| `tuido path [query]` | print the resolved file path |
| `tuido use [workspace]` | switch or show the current workspace |
| `tuido sync [--status]` | blocking fetch, rebase and push |
| `tuido id <fuzzy…>` | stamp a short id on a task |
| `tuido upgrade [--check]` | install the latest release |
| `tuido init` | first-run setup |

Flags may appear before or after the text — `tuido add rotate certs -p high`
works. `--` ends flag parsing.

Every command documents itself, with argument syntax and worked examples:

```sh
tuido help              # the command list
tuido help add          # one command, in detail
tuido add --help        # identical
```

Exit codes: `0` ok, `1` user error, `2` internal, `3` file conflicted,
`4` not initialised. Help exits `0`; a bad flag exits `1`. Errors go to stderr,
output to stdout, and colour is used only when stdout is a terminal — so
scripts and agents get clean, parseable output with no special flags.

## How it stores things

A workspace is a directory; a list is a markdown file.

```
~/notes/todo/            git repo root
├── work/                workspace
│   ├── inbox.md         default capture target
│   └── oncall.md
└── personal/
    └── inbox.md
```

Config is machine-local and lives outside the task repo, so the repo holds only
tasks:

- `~/.config/tuido/config.toml`
- `~/.local/state/tuido/context` — the current workspace
- `~/.cache/tuido/sync.json` — sync state
- `~/.cache/tuido/update.json` — the cached answer to "is there a newer release?"

Anything that should travel *with* your tasks goes in a per-file marker comment,
which syncs for free:

```markdown
<!-- tuido: sort=created capture=backlog -->
```

## Dialect

The [Obsidian Tasks](https://publish.obsidian.md/tasks/) emoji format. All of it
is parsed and preserved; a subset is interpreted.

| | | |
|---|---|---|
| `[ ]` `[/]` `[x]` `[-]` | state | open, in progress, done, cancelled |
| `🔺 ⏫ 🔼 🔽 ⏬` | priority | no marker = normal |
| `📅` due · `➕` created · `✅` done · `❌` cancelled | dates | `YYYY-MM-DD` |
| `🛫` start · `⏳` scheduled | dates | hidden from `ls` until the date |
| `⛔` blocked by · `🆔` id | dependencies | ids are never written automatically |
| `🔁` recurrence · `🏁` on completion | | preserved, not yet acted on |
| `#tag` | tag | stays inline in the description |

### Shorthand

Typing emoji in an editor is friction, so `tuido fmt` accepts a plain-ASCII
input dialect and canonicalises it. You write:

```markdown
- [ ] rotate the token for x :p2 :due monday
```

and `tuido fmt` rewrites it to:

```markdown
- [ ] rotate the token for x ⏫ 📅 2026-08-10 ➕ 2026-08-08
```

| token | meaning |
|---|---|
| `:p1` … `:p5` | priority, highest → lowest |
| `:prio <v>` | priority by name or digit 1–5 |
| `:due <when>` | due date |
| `:start <when>` | start date |
| `:sched <when>` | scheduled date |
| `:new` | nothing but the ➕ creation stamp |
| `:done` | `[x]` plus ✅ today |
| `:drop` / `:cancel` | `[-]` plus ❌ today |

`<when>` is `YYYY-MM-DD`, `today`, `tomorrow`, `week` or a weekday name (the
next strictly future one). Shorthand is input, never storage: the file only
ever contains the emoji dialect, so Obsidian keeps working. A task that had
capture shorthand applied gets a ➕ creation date if it lacks one — shorthand
marks the line as freshly captured, and bare lines are never stamped, so
pre-existing tasks are not backdated. `:done` and `:drop` deliberately don't
stamp ➕ either: closing a task says nothing about when it was written. A token
that cannot apply (field already set, bad value, unknown key) stays in the text
verbatim and is reported. Running `fmt` twice changes nothing.

The laziest way to type a new task is a bare dash bullet:

```markdown
- rotate the token for x
```

A dash bullet in a todo list only ever means a task, and it is invisible to
every task parser — including Obsidian — until it has a checkbox. So `fmt`
turns it (and the `- []` near-miss) into `- [ ] rotate the token for x` and
stamps it as created today. Two escape hatches are deliberate: `*` and `+`
bullets are never touched, so notes stay notes, and a `- [...` bullet is left
alone, so markdown links survive. Outside `fmt`, nothing ever rewrites these
lines.

To expand on every save in Neovim, filter the buffer through `tuido fmt -`.
Pair it with a `BufWritePost` hook running `tuido _commit <file>` (fire and
forget) and editor saves also commit and push in the background, exactly like
edits made through tuido commands:

```lua
vim.api.nvim_create_autocmd("BufWritePre", {
  pattern = vim.fn.expand("~/todos") .. "/*.md", -- your todo root
  callback = function(ev)
    if vim.fn.executable("tuido") == 0 then return end
    local lines = vim.api.nvim_buf_get_lines(ev.buf, 0, -1, false)
    local res = vim.system({ "tuido", "fmt", "-" },
      { stdin = table.concat(lines, "\n") .. "\n" }):wait()
    if res.code ~= 0 then
      vim.notify("tuido fmt: " .. vim.trim(res.stderr or ""), vim.log.levels.WARN)
      return
    end
    local out = vim.split(res.stdout, "\n")
    if out[#out] == "" then table.remove(out) end
    if not vim.deep_equal(out, lines) then
      vim.api.nvim_buf_set_lines(ev.buf, 0, -1, false, out)
    end
  end,
})

vim.api.nvim_create_autocmd("BufWritePost", {
  pattern = vim.fn.expand("~/todos") .. "/*.md",
  callback = function(ev)
    if vim.fn.executable("tuido") == 0 then return end
    vim.system({ "tuido", "_commit", vim.api.nvim_buf_get_name(ev.buf) })
  end,
})
```

### Don't hard-wrap task lines

A task's fields only count on the `- [ ]` line itself — that is how the Obsidian
Tasks format works, and tuido follows it. Indented lines below a task are
preserved and travel with it when sorting, but they are prose: a hard wrap that
pushes `📅 2026-08-11` onto a continuation line silently turns the due date
into text.

So exempt your todo directory from any editor rule that reformats markdown to a
fixed width, and use visual-only soft wrap instead. In Neovim, if an autocmd
sets `textwidth` for markdown, skip it for todo files:

```lua
vim.api.nvim_create_autocmd("FileType", {
  pattern = "markdown",
  callback = function(ev)
    local path = vim.api.nvim_buf_get_name(ev.buf)
    if vim.startswith(path, vim.fn.expand("~/todos") .. "/") then
      vim.opt_local.wrap = true       -- soft wrap, file stays one line per task
      vim.opt_local.linebreak = true
      vim.opt_local.breakindent = true
      return
    end
    vim.opt_local.textwidth = 80
    vim.opt_local.formatoptions:append("t")
  end,
})
```

Long content belongs in indented continuation lines under the task; the task
line itself stays a short description plus its fields. As a safety net,
`tuido fmt` warns when a continuation line carries a field marker or shorthand
token — almost always the debris of an accidental hard wrap.

## Design

Three properties are load-bearing.

**Round-trip fidelity outranks features.** Parsing a file and writing it back is
byte-identical unless a task was deliberately changed. Only mutated lines are
re-serialised, so your hand-written field order, unusual bullets and unknown
emoji survive untouched, and a diff is the size of the actual change. This is
enforced by a golden corpus and a fuzzer asserting both round-trip identity and
canonical stability.

**Structure you wrote is inviolable**, as the sort example above shows. Two
corollaries that example does not: a task-shaped line inside a fenced code
block is never touched, and nested subtasks travel with their parent.

**Never block on the network.** Git is automatic but asynchronous: the local
commit is synchronous and offline, while fetch and push happen in a detached
background process whose outcome is surfaced as a warning line on the next
command. Nothing retries in silence.

And one rule that follows from all three: **fail loudly, never guess.** An
ambiguous query gets a picker or an error, never a silently-chosen default. A
file with conflict markers is refused outright. A line tuido cannot rewrite
without changing its meaning is reported instead of mangled.

## Not implemented

Recurrence *generation* (`🔁` rules are parsed and preserved), `🏁` actions,
archiving, dictation, and sorting across blank lines. None of them require a
file-format change to add later.

## Development

User-visible changes are maintained in [CHANGELOG.md](CHANGELOG.md). Add them
under `Unreleased`, then move them into a dated version section when releasing.

```sh
make test     # everything
make fuzz     # 60s on the parser — the highest-value test here
make check    # vet + test + gofmt
```

Releasing is one command on your own machine — there is no CI:

```sh
make release TAG=v0.3.1
```

That tags, pushes, cross-compiles `darwin`/`linux` × `arm64`/`amd64`, writes
`checksums.txt`, and creates the GitHub release with the binaries attached.
`tuido upgrade` consumes exactly those assets, so the names in the Makefile and
in `selfupdate.AssetName` have to stay in step.

## License

MIT
