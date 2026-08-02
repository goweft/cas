-- wordcount.lua — line, word, and character counts for the active workspace.
--
-- Registers both `wordcount` and the short alias `wc`. Two registrations
-- sharing one handler is the idiomatic way to alias a command.

local function counts()
    local ws = cas.active()
    if not ws then
        cas.reply("No active workspace.")
        return
    end

    local content = ws.content or ""

    local words = 0
    for _ in string.gmatch(content, "%S+") do
        words = words + 1
    end

    -- Count lines the way an editor does: a trailing newline does not
    -- start an extra line.
    local lines = 0
    for _ in string.gmatch(content, "\n") do
        lines = lines + 1
    end
    if #content > 0 and string.sub(content, -1) ~= "\n" then
        lines = lines + 1
    end

    cas.reply("**" .. ws.title .. "** — " .. lines .. " lines · "
        .. words .. " words · " .. #content .. " characters")
end

cas.command("wordcount", "Line/word/char counts for the active workspace", counts)
cas.command("wc", "Alias for wordcount", counts)
