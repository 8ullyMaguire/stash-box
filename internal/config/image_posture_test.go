package config

import "testing"

// Image bytes are bytes, so the image switch is the same class of posture as the content plane's, and its
// SHIPPED state is the assertion that matters: this is the value an instance runs with when no config
// file names the key.
//
// The key was in a deployed config (`images_enabled: false`) before any code read it, which is why the
// default is asserted here rather than left to the file: a switch whose only existence is a line somebody
// wrote is a line, not a posture. See plan phase 10, slice 10a-ii.
func TestServingImagesIsOffInTheShippedDefault(t *testing.T) {
	if C.ImagesEnabled {
		t.Fatal("images_enabled defaults to true: an instance that has not been told to serve image " +
			"bytes would hand out a copy of every image it holds a row for")
	}
}

// The media DIRECTORY is unset in the shipped default, and that is a refusal rather than a default
// path: an instance told to serve images without being told where from refuses by naming the key
// (internal/service/mediaplane.MediaDirError), which is the same shape as the switch itself -- the key
// that lived in a deployed config for two years before any code read it (slice 10b-1).
func TestTheMediaDirIsUnsetInTheShippedDefault(t *testing.T) {
	if C.MediaDir != "" {
		t.Fatalf("media_dir defaults to %q: every instance that has not named a directory would serve "+
			"from a path it never chose", C.MediaDir)
	}
}

// The accessor reads its OWN field, for the reason the image switch's does: a Getter that fell back to
// the content plane's storage path would be a wire crossed invisibly, and this key is read by the
// request-time gate.
func TestTheMediaDirIsReadFromItsOwnField(t *testing.T) {
	previous := C.MediaDir
	t.Cleanup(func() { C.MediaDir = previous })

	C.MediaDir = "/tmp/one"
	if GetMediaDir() != "/tmp/one" {
		t.Errorf("GetMediaDir() = %q with the field set to /tmp/one", GetMediaDir())
	}
	C.MediaDir = ""
	if GetMediaDir() != "" {
		t.Errorf("GetMediaDir() = %q with the field clear", GetMediaDir())
	}
}

// The accessor returns the field AS WRITTEN, without cleaning or absolutising it: the check that decides
// whether a path is usable lives in internal/service/mediaplane, so that there is one place that decides
// and the startup line and the gate cannot disagree. Asserted with a path only cleaning would change.
func TestTheAccessorDoesNotCleanThePath(t *testing.T) {
	previous := C.MediaDir
	t.Cleanup(func() { C.MediaDir = previous })

	C.MediaDir = "/tmp/media/../media/"
	if GetMediaDir() != "/tmp/media/../media/" {
		t.Errorf("GetMediaDir() = %q, want the field verbatim", GetMediaDir())
	}
}

// The accessor reads its OWN field, not the neighbouring content_delivery. A switch that read the other
// bool would be a switch that does not control what its name says -- and the two are OR'd together in
// mediaplane, where a crossed wire would be invisible: both keys off, both keys on, and one of the four
// mixed states would quietly do the wrong thing.
func TestServingImagesIsReadFromItsOwnField(t *testing.T) {
	previousImages, previousContent := C.ImagesEnabled, C.ContentDelivery
	t.Cleanup(func() { C.ImagesEnabled, C.ContentDelivery = previousImages, previousContent })

	C.ImagesEnabled, C.ContentDelivery = false, false
	if GetImagesEnabled() {
		t.Error("GetImagesEnabled() = true with both fields clear")
	}

	C.ImagesEnabled, C.ContentDelivery = true, false
	if !GetImagesEnabled() {
		t.Error("GetImagesEnabled() = false with images_enabled set and content_delivery clear")
	}

	C.ImagesEnabled, C.ContentDelivery = false, true
	if GetImagesEnabled() {
		t.Error("GetImagesEnabled() = true with only content_delivery set: the accessor reads the " +
			"neighbouring key")
	}
}
