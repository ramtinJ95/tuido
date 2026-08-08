package main

import "github.com/spf13/pflag"

// Flag registration lives here, once per command, so that `tuido <cmd> --help`
// and `tuido help <cmd>` are guaranteed to describe the same flags.

type commonFlags struct {
	root *string
	ws   *string
}

func registerCommon(fs *pflag.FlagSet) commonFlags {
	return commonFlags{
		root: fs.String("root", "", "override the todo root for this invocation"),
		ws:   fs.StringP("workspace", "w", "", "workspace to operate on"),
	}
}

type initFlags struct {
	root, workspace, remote *string
	git, noGit, force       *bool
}

func registerInit(fs *pflag.FlagSet) initFlags {
	return initFlags{
		root:      fs.String("root", "", "todo root directory"),
		workspace: fs.String("workspace", "", `first workspace name (default "work")`),
		git:       fs.Bool("git", true, "initialise a git repo in the root"),
		noGit:     fs.Bool("no-git", false, "do not initialise a git repo"),
		remote:    fs.String("remote", "", "git remote; if it has commits, clone it instead of initialising"),
		force:     fs.Bool("force", false, "overwrite an existing config"),
	}
}

type addFlags struct {
	commonFlags
	prio, due, list *string
	tags            *[]string
	all             *bool
}

func registerAdd(fs *pflag.FlagSet) addFlags {
	return addFlags{
		commonFlags: registerCommon(fs),
		prio:        fs.StringP("prio", "p", "", "highest|high|medium|normal|low|lowest"),
		due:         fs.StringP("due", "d", "", "YYYY-MM-DD | today | tomorrow | week | <weekday>"),
		tags:        fs.StringSliceP("tag", "t", nil, "tag to append (repeatable)"),
		list:        fs.StringP("list", "l", "", "destination list, or list/section"),
		all:         fs.Bool("all", false, "search every workspace for the destination list"),
	}
}

type lsFlags struct {
	commonFlags
	all   *bool
	tags  *[]string
	dueBy *string
}

func registerLs(fs *pflag.FlagSet) lsFlags {
	return lsFlags{
		commonFlags: registerCommon(fs),
		all:         fs.Bool("all", false, "show everything, in every workspace"),
		tags:        fs.StringSliceP("tag", "t", nil, "only tasks with this tag (repeatable)"),
		dueBy:       fs.String("due", "", "only tasks due on or before this date (today|tomorrow|week|YYYY-MM-DD)"),
	}
}

type sortFlags struct {
	commonFlags
	by  *string
	all *bool
}

func registerSort(fs *pflag.FlagSet) sortFlags {
	return sortFlags{
		commonFlags: registerCommon(fs),
		by:          fs.String("by", "", "prio|due|created|none (default: the file's marker, else prio)"),
		all:         fs.Bool("all", false, "sort every workspace"),
	}
}

// openFlags is the one command that spends --root on a boolean, so it does not
// take the common flags.
type openFlags struct {
	ws        *string
	wholeRepo *bool
	all       *bool
}

func registerOpen(fs *pflag.FlagSet) openFlags {
	return openFlags{
		ws:        fs.StringP("workspace", "w", "", "workspace to search"),
		wholeRepo: fs.BoolP("root", "r", false, "open the repo root instead of a single list"),
		all:       fs.Bool("all", false, "search every workspace"),
	}
}

// scopeFlags covers the commands whose only extra option is how wide to search.
type scopeFlags struct {
	commonFlags
	all *bool
}

func registerScope(fs *pflag.FlagSet) scopeFlags {
	return scopeFlags{
		commonFlags: registerCommon(fs),
		all:         fs.Bool("all", false, "search every workspace"),
	}
}

type syncFlags struct {
	commonFlags
	status *bool
}

func registerSync(fs *pflag.FlagSet) syncFlags {
	return syncFlags{
		commonFlags: registerCommon(fs),
		status:      fs.Bool("status", false, "print sync state without touching the network"),
	}
}

// registerFlags declares a command's flags on fs without running it, so help
// can be rendered for a command that is not being executed.
func registerFlags(name string, fs *pflag.FlagSet) {
	switch name {
	case "init":
		registerInit(fs)
	case "add":
		registerAdd(fs)
	case "ls":
		registerLs(fs)
	case "sort":
		registerSort(fs)
	case "open":
		registerOpen(fs)
	case "sync":
		registerSync(fs)
	case "use":
		registerCommon(fs)
	case "done", "path", "id":
		registerScope(fs)
	}
}
