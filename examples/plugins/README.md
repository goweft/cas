# Example plugins

Ready-to-use Lua plugins for CAS. Copy any of them into your plugin
directory and they're live on the next start — no recompile:

```bash
mkdir -p ~/.cas/plugins
cp examples/plugins/*.lua ~/.cas/plugins/
```

| Command | File | What it does |
|---|---|---|
| `standup` | `standup.lua` | One-line status of every open workspace |
| `wordcount`, `wc` | `wordcount.lua` | Line/word/char counts for the active workspace |
| `toc` | `toc.lua` | Table of contents from the active workspace's markdown headings |
| `todos` | `todos.lua` | TODO/FIXME/HACK markers across all workspaces, with line numbers |

These files are loaded and executed by the test suite
(`internal/plugin/examples_test.go`), so they are verified by CI on every
push — if the plugin API changes, the examples break loudly instead of
rotting quietly.

## The API, in brief

A plugin registers commands at load time and does its work in handlers:

```lua
cas.command("name", "description", function()
    -- runtime API, available inside handlers:
    local all = cas.workspaces()  -- array of {id, type, title, content}
    local ws  = cas.active()      -- most recent workspace, or nil
    cas.reply("markdown text")    -- what the user sees
end)
```

Rules of the sandbox:

- Available Lua libraries: `base`, `table`, `string`, `math`. No file I/O,
  no `os`, no network, no `dofile`/`loadfile`.
- Handlers take **no arguments** — the user's message is not passed in.
  Commands match by exact name or prefix (`wc please` runs `wc`), and the
  handler works off workspace state, not message text.
- Workspace data is read-only. A plugin's only output channel is
  `cas.reply(text)`; the text is rendered as markdown in the chat panel.
- Matching is case-insensitive; if two plugins register the same command
  name, the last one loaded wins.
