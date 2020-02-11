package log

// Log levels defined for use with:
//   klog.V(__).Info

const (
	// INFO : This level is for anything which does not happen very often, and while
	//        not being an error, is important information and helpful to be in the
	//        logs, eg:
	//          * startup information
	//          * HA failovers
	//          * re-connections/disconnections
	//          * ...
	//        This level is not specifically defined as you would use the
	//        klog.Info helpers
	//
	// DEBUG : used to provide logs for often occurring events that could be helpful
	//        for debugging errors.
	DEBUG = 2
	// TRACE : used for logging that occurs often or may dump a lot of information
	//         which generally would be less useful for debugging but can be useful
	//         in some cases, for example tracing function entry/exit, parameters,
	//         structures, etc..
	TRACE = 3
)
