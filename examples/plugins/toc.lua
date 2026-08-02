-- toc.lua — table of contents from the active workspace's markdown headings.
--
-- Skips headings inside fenced code blocks, so a `# comment` in a code
-- sample doesn't show up as a section.

cas.command("toc", "Table of contents for the active workspace", function()
    local ws = cas.active()
    if not ws then
        cas.reply("No active workspace.")
        return
    end

    local out = {}
    local in_fence = false
    for line in string.gmatch((ws.content or "") .. "\n", "(.-)\n") do
        if string.match(line, "^```") then
            in_fence = not in_fence
        elseif not in_fence then
            local hashes, title = string.match(line, "^(#+)%s+(.+)$")
            if hashes and #hashes <= 6 then
                out[#out + 1] = string.rep("  ", #hashes - 1) .. "- " .. title
            end
        end
    end

    if #out == 0 then
        cas.reply("No markdown headings in \"" .. ws.title .. "\".")
    else
        cas.reply("## Contents — " .. ws.title .. "\n\n" .. table.concat(out, "\n"))
    end
end)
