// Package monogatari is the story library for apps built on ochazuke: the
// story format, the runner and driver, recordings and trimming, explore, and
// the host an app mounts to build its own story tester.
//
// An app links monogatari only in its tester build, behind the build tag
// "monogatari"; a normal build of the app links none of it. ochazuke's own
// o-story is one more host of this library.
//
// This is the first commit of the repository (lane L0 of the plan): the
// engine moves in from ochazuke's story and explore packages in L1.
package monogatari
