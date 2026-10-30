// Package monogatari is the story library for apps built on ochazuke: the
// story format, the runner and driver, recordings and trimming, explore, and
// the host an app mounts to build its own story tester.
//
// An app links monogatari only in its tester build, behind the build tag
// "monogatari"; a normal build of the app links none of it. ochazuke's own
// o-story is one more host of this library.
//
// A story is data (a script of events and what must hold afterwards, plus
// the app, fixture and seed that build its starting state); Library.Build
// turns it into a live runtime and model through the app's fixture, a
// Driver plays events against it headless, and a Recorder turns the
// messages of a live session into the same events. State changes only
// through events; a frame is a pure function of the story and its events.
// Stories never read production state: a fixture gets only what Env gives
// it.
package monogatari
