DXBerry-Pi
1. Rename dxberry.txt.example to dxberry.txt and fill it in (Notepad is fine).
2. Put this drive in the Pi, connect Ethernet, power on. First boot takes 5-8 minutes.
3. Open http://<your STATIC_IP>:8080 (or the DHCP address). SSH: root or dietpi with your PASSWORD.
dxberry-status.txt is written to this partition after every run, successful or not: it lists what was
applied and any step that failed. dxberry-ERROR.txt appears only when dxberry.txt itself could not be used.
If the Pi does not come up at the address you set, read dxberry-ERROR.txt on this partition first: it names the exact dxberry.txt line that was rejected.

Radios: plug in your sound card / CAT / PTT interface, then over SSH run "sudo dxberry-radio scan" to
list what was found, "sudo dxberry-radio add radio1 --audio N --cat N" to pin one, and
"sudo dxberry-radio claim radio1 graywolf" to hand it to Graywolf. A pinned radio always appears as
sound card hw:RADIO1 and, when it has a CAT port, /dev/dxberry/radio1-cat. "sudo dxberry-radio status"
shows what is plugged in and who owns it.
