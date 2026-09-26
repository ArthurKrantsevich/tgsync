package permissions

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// destroyCmds delete, overwrite or stop things whatever their arguments,
// or run another command the check cannot see into.
var destroyCmds = map[string]bool{
	"rm": true, "rmdir": true, "unlink": true, "mv": true, "dd": true, "shred": true, "truncate": true,
	"chmod": true, "chown": true, "chgrp": true, "ln": true,
	"kill": true, "pkill": true, "killall": true, "shutdown": true, "reboot": true, "poweroff": true, "halt": true,
	"systemctl": true, "launchctl": true, "crontab": true, "mount": true, "umount": true, "fdisk": true, "parted": true,
	"sudo": true, "doas": true, "su": true, "ssh": true, "scp": true, "rsync": true,
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true, "pwsh": true, "powershell": true, "cmd": true,
	"eval": true, "exec": true, "source": true, ".": true, "xargs": true, "env": true, "command": true, "builtin": true,
}

// destroySubs are subcommands of git, docker and similar tools that lose
// work, publish it or delete containers and images.
var destroySubs = map[string]map[string]bool{
	"git": {"push": true, "reset": true, "clean": true, "checkout": true, "restore": true, "rebase": true,
		"rm": true, "filter-branch": true, "filter-repo": true, "gc": true, "prune": true, "update-ref": true},
	"docker":  {"rm": true, "rmi": true, "prune": true, "system": true, "volume": true, "kill": true, "stop": true},
	"podman":  {"rm": true, "rmi": true, "prune": true, "system": true, "volume": true, "kill": true, "stop": true},
	"kubectl": {"delete": true, "apply": true, "drain": true, "scale": true},
	"npm":     {"publish": true, "unpublish": true},
}

// gitDestroyArgs turn an otherwise harmless git subcommand destructive:
// git branch -D, git stash drop, git tag -d.
var gitDestroyArgs = map[string][]string{
	"branch": {"-D", "-d", "--delete", "-M", "-f", "--force"},
	"stash":  {"drop", "clear"},
	"tag":    {"-d", "--delete", "-f"},
}

// destructive reports whether an auto-approved command should still be
// shown to the user in full: it deletes or overwrites data, stops
// processes, publishes work or runs code the check cannot read. Commands
// that do not parse, or whose name is built at run time, count as
// destructive. Output redirection does not: agents write logs and scratch
// files that way all the time.
func destructive(cmd string) bool {
	f, err := syntax.NewParser().Parse(strings.NewReader(cmd), "")
	if err != nil {
		return true
	}
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		if found {
			return false
		}
		if ce, ok := n.(*syntax.CallExpr); ok && len(ce.Args) > 0 {
			found = destructiveCall(ce)
		}
		return !found
	})
	return found
}

func destructiveCall(ce *syntax.CallExpr) bool {
	var raw strings.Builder
	if err := syntax.NewPrinter().Print(&raw, ce.Args[0]); err != nil || strings.ContainsAny(raw.String(), "$`") {
		return true // the command name is built at run time
	}
	name := cmdName(&syntax.CallExpr{Args: []*syntax.Word{{Parts: []syntax.WordPart{&syntax.Lit{Value: unquote(raw.String())}}}}})
	if destroyCmds[name] || strings.HasPrefix(name, "mkfs") {
		return true
	}
	args := make([]string, 0, len(ce.Args)-1)
	for _, a := range ce.Args[1:] {
		args = append(args, unquote(a.Lit()))
	}
	if name == "find" {
		for _, a := range args {
			if a == "-delete" || strings.HasPrefix(a, "-exec") || strings.HasPrefix(a, "-ok") {
				return true
			}
		}
		return false
	}
	subs, ok := destroySubs[name]
	if !ok {
		return false
	}
	sub, rest := "", []string(nil)
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			sub, rest = a, args[i+1:]
			break
		}
	}
	if subs[sub] {
		return true
	}
	if name == "git" {
		for _, bad := range gitDestroyArgs[sub] {
			for _, a := range rest {
				if a == bad {
					return true
				}
			}
		}
	}
	return false
}
