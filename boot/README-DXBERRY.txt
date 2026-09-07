DXBerry-Pi
1. Rename dxberry.txt.example to dxberry.txt and fill it in (Notepad is fine).
2. Put this drive in the Pi, connect Ethernet, power on. First boot takes 5-8 minutes.
3. Open http://<your STATIC_IP>:8080 (or the DHCP address). SSH: root or dietpi with your PASSWORD.
dxberry-status.txt is written to this partition after every run, successful or not: it lists what was
applied and any step that failed. dxberry-ERROR.txt appears only when dxberry.txt itself could not be used.
