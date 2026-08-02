-- standup.lua — one-line status of every open workspace.
--
-- This is the plugin from the README's Plugins section, with an
-- empty-state guard added. Type `standup` in the chat to run it.

cas.command("standup", "Daily standup from workspaces", function()
    local ws = cas.workspaces()
    if #ws == 0 then
        cas.reply("No workspaces yet.")
        return
    end
    local lines = {}
    for i, w in ipairs(ws) do
        lines[i] = "- " .. w.title .. " (" .. w.type .. ")"
    end
    cas.reply("## Standup\n\n" .. table.concat(lines, "\n"))
end)
