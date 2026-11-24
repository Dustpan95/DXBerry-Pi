"""Describe only the fixture's private Qt/PulseAudio runtime and dummy devices."""
import json
from PyQt5.QtCore import QLibraryInfo, qVersion
from PyQt5.QtWidgets import QApplication
from PyQt5.QtMultimedia import QAudioDeviceInfo

app = QApplication([])
source = QAudioDeviceInfo.defaultInputDevice().deviceName()
sink = QAudioDeviceInfo.defaultOutputDevice().deviceName()
if not source or not sink:
    raise SystemExit("dummy audio devices unavailable")
print(json.dumps({"qt": qVersion(), "input": source, "output": sink,
                  "qt_core": QLibraryInfo.location(QLibraryInfo.LibrariesPath) + "/libQt5Core.so.5"}))
