//go:build !darwin && !windows

package ocr

import "os/exec"

// NotAvailable is what the page says where there is no reader.
const NotAvailable = "Subtitles cannot be read into text on this computer, so they can only be copied as they are. ARFABIT reads them with Tesseract when it is installed."

// System is the reader this computer has: Tesseract, if somebody installed
// it, and otherwise none.
func System() Reader {
	bin, err := exec.LookPath("tesseract")
	if err != nil {
		return nil
	}
	return tesseract{bin: bin}
}
