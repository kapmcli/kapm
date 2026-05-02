Output demo-media/monitor.ascii

Require ./kapm

Set Width 1400
Set Height 900
Set WaitTimeout 15s

Hide
Type "rm -rf /tmp/kapm-vhs-home; mkdir -p /tmp/kapm-vhs-home; export HOME=/tmp/kapm-vhs-home; export PS1='$ '; clear"
Enter
Sleep 200ms
Show

Type "KAPM_UPDATED_AT=12:00:00 ./kapm monitor --since 8760h --logs-dir testdata/monitor/logs --sessions-dir testdata/monitor/sessions --global"
Enter
Wait+Screen /Overview/
Wait+Screen /Summary/
Wait+Screen /Top tools/
Wait+Screen /Activity/
Sleep 1s
Type "2"
Wait+Screen /Last act/
Sleep 1s
Type "3"
Wait+Screen /Agent/
Sleep 1s
Type "4"
Wait+Screen /Calls/
Sleep 1s
Type "5"
Wait+Screen /Skills/
Sleep 2s
Type "q"
