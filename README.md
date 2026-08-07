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
first workspace  [work]: work

✓ wrote ~/.config/tuido/config.toml
✓ context set to `work`
```

On a second machine, point `--remote` at the repo you already have. It clones
rather than initialising an empty one beside it:

```sh
tuido init --root ~/notes/todo --remote git@github.com:you/todo.git
```

[fzf](https://github.com/junegunn/fzf) is optional. With it on `PATH`, an
ambiguous query opens a picker; without it, tuido prints the candidates and
exits 1, so every command stays scriptable.

## Commands

| | |
|---|---|
| `tuido add [flags] <text…>` | capture a task (`-p` prio, `-d` due, `-t` tag, `-l` list) |
| `tuido done <fuzzy…>` | mark a task done |
| `tuido ls [list] [--all]` | show actionable tasks |
| `tuido sort [list] [--by …]` | reorder tasks within their blocks |
| `tuido open [query] [--root]` | open a list, or the whole repo, in `$EDITOR` |
| `tuido path [query]` | print the resolved file path |
| `tuido use [workspace]` | switch or show the current workspace |
| `tuido sync [--status]` | blocking fetch, rebase and push |
| `tuido id <fuzzy…>` | stamp a short id on a task |
| `tuido init` | first-run setup |

Flags may appear before or after the text — `tuido add rotate certs -p high`
works. `--` ends flag parsing.

Exit codes: `0` ok, `1` user error, `2` internal, `3` file conflicted,
`4` not initialised.

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

## Design

Three properties are load-bearing.

**Round-trip fidelity outranks features.** Parsing a file and writing it back is
byte-identical unless a task was deliberately changed. Only mutated lines are
re-serialised, so your hand-written field order, unusual bullets and unknown
emoji survive untouched, and a diff is the size of the actual change. This is
enforced by a golden corpus and a fuzzer asserting both round-trip identity and
canonical stability.

**Structure you wrote is inviolable.** Headings, prose, blank lines and code
fences are fences. `sort` reorders task lines *within* a run of them and nothing
else — a task-shaped line inside a ``` block is never touched, and nested
subtasks travel with their parent. Sorting twice produces identical bytes.

**Never block on the network.** Git is automatic but asynchronous: the local
commit is synchronous and offline, while fetch and push happen in a detached
background process whose outcome is surfaced as a warning line on the next
command. Nothing retries in silence.

And one rule that follows from all three: **fail loudly, never guess.** An
ambiguous query gets a picker or an error, never a silently-chosen default. A
file with conflict markers is refused outright. A line tuido cannot rewrite
without changing its meaning is reported instead of mangled.

## Not in v1

Recurrence *generation* (`🔁` rules are parsed and preserved), `🏁` actions,
archiving, dictation, and sorting across blank lines. None of them require a
file-format change to add later.

## Development

```sh
make test     # everything
make fuzz     # 60s on the parser — the highest-value test here
make check    # vet + test + gofmt
```

## License

MIT
