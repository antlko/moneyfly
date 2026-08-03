// Package scheduler runs the daily rate refresh and the nightly backup.
//
// Stage 07 fills it in. Nothing in the request path ever waits on a provider: the
// scheduler is the only network caller.
package scheduler
