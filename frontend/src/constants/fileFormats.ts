// - Printer file formats (extensions without the dot) ------------
// Text g-code (FDM) - the server reads print parameters out of these (gcode_meta).
export const GCODE_FORMATS = ['gcode', 'gco', 'g']

// Further FDM slicer/print-job formats (binary, no parameters extracted).
export const FDM_JOB_FORMATS = ['bgcode', 'gx', 'g3drem', 'ufp', 'makerbot']

// Resin/MSLA slicer output. The server reads print parameters from all of these.
export const RESIN_FORMATS = ['pwmx', 'pwmo', 'pws', 'pw0', 'pwms', 'pwmb', 'sl1', 'sl1s', 'ctb', 'cbddlp', 'photon']

// Resin formats the server can rebuild a mesh from - only these get the 3D button.
// The layer decoder is Anycubic-PWMX-specific; everything else would 422.
export const RESIN_VIEWER_FORMATS = ['pwmx']

// Extensions the desktop slicers can open: meshes and CAD exchange formats plus
// the text/binary g-code they load for preview. Resin formats are absent - none
// of the three slicers reads them.
export const SLICER_FORMATS = ['stl', '3mf', 'obj', 'step', 'stp', 'amf', ...GCODE_FORMATS, 'bgcode']
// No `accept` on the upload pickers on purpose: the server stores whatever it is
// sent (only .zip is treated specially, by being extracted), so filtering the
// dialog just hid CAD sources like .FCStd, .step or .f3d from the file chooser
// while drag-and-drop accepted them anyway. Files are served back as
// application/octet-stream attachments, so a wider set of extensions carries no
// extra risk - see mimeForExt in backend/internal/api/designfiles.go.

export const lowerExt = (name: string) => (name.split('.').pop() || '').toLowerCase()

// Bambu Studio / OrcaSlicer / PrusaSlicer all share one downloader that matches
// the URL scheme `<slicer>://open?file=<percent-encoded download URL>`
// (regex ^(orcaslicer|prusaslicer|bambustudio|cura)://open[/]?\?file= in their
// Downloader.cpp). The download URL must be percent-encoded, as Printables emits.
export const SLICERS = [
  { name: 'Bambu Studio', scheme: (url: string) => `bambustudio://open?file=${encodeURIComponent(url)}` },
  { name: 'OrcaSlicer',   scheme: (url: string) => `orcaslicer://open?file=${encodeURIComponent(url)}` },
  { name: 'PrusaSlicer',  scheme: (url: string) => `prusaslicer://open?file=${encodeURIComponent(url)}` },
]
