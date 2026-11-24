package rig

import "github.com/Dustpan95/DXBerry-Pi/station/internal/radios"

// MatchesCommand compares the procfs argv captured in the fresh peer identity
// with a separately validated registration command. It performs no I/O and
// grants no freshness, program-free or physical-device authority by itself.
func (s ServiceObservation) MatchesCommand(command radios.RigCommand) bool {
	return s.reader != nil && s.peer.Same(s.peer) && s.radio == command.RadioName() &&
		int(s.peer.endpoint.Peer().Port()) == command.Port() && command.MatchesArgumentsDigest(s.peer.arguments)
}
