Output demo-media/demo.gif

Require ./kapm

Set Width 1920
Set Height 1080
Set FontSize 18
Set WaitTimeout 10s

Hide
Type "export PS1='$ '; clear"
Enter
Show

Sleep 1s

Type "./kapm version"
Enter
Sleep 2.5s

Type "./kapm monitor --since 8760h --logs-dir testdata/monitor/logs"
Enter
Wait+Screen /Overview/
Sleep 2.5s

# Sessions tab
Type "2"
Wait+Screen /Last act/
Sleep 2.5s

# Enter session detail
Enter
Sleep 1s
Type "j"
Sleep 300ms
Type "j"
Sleep 300ms
Type "j"
Sleep 300ms
Type "j"
Sleep 300ms
Type "j"
Sleep 2.5s

# Back to list
Escape
Sleep 1s

# Agents tab
Type "3"
Wait+Screen /Agent/
Sleep 2.5s

# Tools tab
Type "4"
Wait+Screen /Calls/
Sleep 2.5s

# Skills tab
Type "5"
Wait+Screen /Skills/
Sleep 2.5s

Type "q"
Sleep 500ms
