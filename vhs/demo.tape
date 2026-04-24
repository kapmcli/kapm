Output vhs/demo.gif

Require ./kapm

Set Width 1400
Set Height 900
Set FontSize 18
Set WaitTimeout 10s

Sleep 1s

Type "./kapm version"
Enter
Sleep 2s

Type "./kapm sync --help"
Enter
Sleep 3s

Type "./kapm monitor --since 8760h --logs-dir testdata/monitor/logs"
Enter
Wait+Screen /Overview/
Sleep 2s
Type "2"
Wait+Screen /Last act/
Sleep 2s
Type "q"
Sleep 1s
