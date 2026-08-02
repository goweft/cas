-- todos.lua — collect TODO / FIXME / HACK markers across all workspaces.
--
-- Scans every open workspace and groups hits by workspace title with
-- real line numbers (blank lines count, so the numbers match an editor).

cas.command("todos", "List TODO/FIXME/HACK markers across all workspaces", function()
    local sections = {}

    for _, ws in ipairs(cas.workspaces()) do
        local hits = {}
        local n = 0
        for line in string.gmatch((ws.content or "") .. "\n", "(.-)\n") do
            n = n + 1
            if string.match(line, "TODO") or string.match(line, "FIXME")
                or string.match(line, "HACK") then
                local trimmed = string.match(line, "^%s*(.-)%s*$")
                hits[#hits + 1] = "- L" .. n .. ": " .. trimmed
            end
        end
        if #hits > 0 then
            sections[#sections + 1] = "### " .. ws.title .. "\n"
                .. table.concat(hits, "\n")
        end
    end

    if #sections == 0 then
        cas.reply("No TODO/FIXME/HACK markers in any workspace.")
    else
        cas.reply("## Open markers\n\n" .. table.concat(sections, "\n\n"))
    end
end)
