#!/bin/bash
set -u
# Load the unchanged runner and real module loader; never prepare a radio.
# shellcheck source=provision/lib/common.sh
source "$DXB_LIB/common.sh"
# shellcheck source=provision/lib/radio.sh
source "$DXB_LIB/radio.sh"
# shellcheck source=provision/lib/apps.sh
source "$DXB_LIB/apps.sh"
# shellcheck source=provision/lib/screen.sh
source "$DXB_LIB/screen.sh"
dxb_screen_session_run wsjtx-fixture1
