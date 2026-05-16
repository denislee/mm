// Package fonts enumerates a curated list of common system fonts shipped on
// Linux distributions. It returns only the entries whose files actually exist
// on disk, so the mm settings dropdown can show concrete choices the
// user can select without typing a path.
package fonts

import (
	"os"
	"path/filepath"
)

// Font is one entry in the font dropdown. Path is the absolute path to a
// .ttf/.otf file, or empty to mean "use the bundled Go font".
type Font struct {
	Name string
	Path string
}

// candidate lists a display name and a set of likely paths across distros.
// The first existing path wins.
type candidate struct {
	name  string
	paths []string
}

var candidates = []candidate{
	{"DejaVu Sans", []string{
		"/usr/share/fonts/TTF/DejaVuSans.ttf",
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/dejavu-sans/DejaVuSans.ttf",
	}},
	{"DejaVu Sans Mono", []string{
		"/usr/share/fonts/TTF/DejaVuSansMono.ttf",
		"/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
		"/usr/share/fonts/dejavu/DejaVuSansMono.ttf",
	}},
	{"DejaVu Serif", []string{
		"/usr/share/fonts/TTF/DejaVuSerif.ttf",
		"/usr/share/fonts/truetype/dejavu/DejaVuSerif.ttf",
		"/usr/share/fonts/dejavu/DejaVuSerif.ttf",
	}},
	{"Liberation Sans", []string{
		"/usr/share/fonts/liberation/LiberationSans-Regular.ttf",
		"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
		"/usr/share/fonts/TTF/LiberationSans-Regular.ttf",
	}},
	{"Liberation Mono", []string{
		"/usr/share/fonts/liberation/LiberationMono-Regular.ttf",
		"/usr/share/fonts/truetype/liberation/LiberationMono-Regular.ttf",
		"/usr/share/fonts/TTF/LiberationMono-Regular.ttf",
	}},
	{"Liberation Serif", []string{
		"/usr/share/fonts/liberation/LiberationSerif-Regular.ttf",
		"/usr/share/fonts/truetype/liberation/LiberationSerif-Regular.ttf",
		"/usr/share/fonts/TTF/LiberationSerif-Regular.ttf",
	}},
	{"Noto Sans", []string{
		"/usr/share/fonts/noto/NotoSans-Regular.ttf",
		"/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf",
		"/usr/share/fonts/TTF/NotoSans-Regular.ttf",
		"/usr/share/fonts/google-noto/NotoSans-Regular.ttf",
	}},
	{"Noto Sans Mono", []string{
		"/usr/share/fonts/noto/NotoSansMono-Regular.ttf",
		"/usr/share/fonts/truetype/noto/NotoSansMono-Regular.ttf",
		"/usr/share/fonts/TTF/NotoSansMono-Regular.ttf",
	}},
	{"Noto Serif", []string{
		"/usr/share/fonts/noto/NotoSerif-Regular.ttf",
		"/usr/share/fonts/truetype/noto/NotoSerif-Regular.ttf",
		"/usr/share/fonts/TTF/NotoSerif-Regular.ttf",
	}},
	{"Cantarell", []string{
		"/usr/share/fonts/cantarell/Cantarell-Regular.otf",
		"/usr/share/fonts/cantarell/Cantarell-VF.otf",
		"/usr/share/fonts/abattis-cantarell/Cantarell-Regular.otf",
		"/usr/share/fonts/OTF/Cantarell-Regular.otf",
	}},
	{"Ubuntu", []string{
		"/usr/share/fonts/truetype/ubuntu/Ubuntu-R.ttf",
		"/usr/share/fonts/TTF/Ubuntu-R.ttf",
	}},
	{"Ubuntu Mono", []string{
		"/usr/share/fonts/truetype/ubuntu/UbuntuMono-R.ttf",
		"/usr/share/fonts/TTF/UbuntuMono-R.ttf",
	}},
	{"JetBrains Mono", []string{
		"/usr/share/fonts/TTF/JetBrainsMono-Regular.ttf",
		"/usr/share/fonts/jetbrains-mono/JetBrainsMono-Regular.ttf",
		"/usr/share/fonts/truetype/jetbrains-mono/JetBrainsMono-Regular.ttf",
		"/usr/share/fonts/TTF/JetBrainsMonoNerdFont-Regular.ttf",
		"/usr/share/fonts/TTF/JetBrainsMonoNerdFontMono-Regular.ttf",
	}},
	{"Fira Sans", []string{
		"/usr/share/fonts/TTF/FiraSans-Regular.ttf",
		"/usr/share/fonts/truetype/fira-sans/FiraSans-Regular.ttf",
		"/usr/share/fonts/OTF/FiraSans-Regular.otf",
	}},
	{"Fira Mono", []string{
		"/usr/share/fonts/TTF/FiraMono-Regular.ttf",
		"/usr/share/fonts/truetype/fira-mono/FiraMono-Regular.ttf",
		"/usr/share/fonts/OTF/FiraMono-Regular.otf",
	}},
	{"Fira Code", []string{
		"/usr/share/fonts/TTF/FiraCode-Regular.ttf",
		"/usr/share/fonts/truetype/firacode/FiraCode-Regular.ttf",
		"/usr/share/fonts/OTF/FiraCode-Regular.otf",
	}},
	{"Hack", []string{
		"/usr/share/fonts/TTF/Hack-Regular.ttf",
		"/usr/share/fonts/truetype/hack/Hack-Regular.ttf",
	}},
	{"Source Code Pro", []string{
		"/usr/share/fonts/OTF/SourceCodePro-Regular.otf",
		"/usr/share/fonts/adobe-source-code-pro/SourceCodePro-Regular.otf",
		"/usr/share/fonts/truetype/source-code-pro/SourceCodePro-Regular.ttf",
	}},
	{"Source Sans Pro", []string{
		"/usr/share/fonts/OTF/SourceSans3-Regular.otf",
		"/usr/share/fonts/OTF/SourceSansPro-Regular.otf",
		"/usr/share/fonts/adobe-source-sans-pro/SourceSansPro-Regular.otf",
	}},
	{"Inter", []string{
		"/usr/share/fonts/inter/Inter-Regular.otf",
		"/usr/share/fonts/TTF/Inter-Regular.ttf",
		"/usr/share/fonts/truetype/inter/Inter-Regular.ttf",
	}},
	{"Roboto", []string{
		"/usr/share/fonts/TTF/Roboto-Regular.ttf",
		"/usr/share/fonts/truetype/roboto/Roboto-Regular.ttf",
		"/usr/share/fonts/google-roboto/Roboto-Regular.ttf",
	}},
	{"Roboto Mono", []string{
		"/usr/share/fonts/TTF/RobotoMono-Regular.ttf",
		"/usr/share/fonts/truetype/roboto-mono/RobotoMono-Regular.ttf",
	}},
	{"IBM Plex Sans", []string{
		"/usr/share/fonts/TTF/IBMPlexSans-Regular.ttf",
		"/usr/share/fonts/truetype/ibm-plex/IBMPlexSans-Regular.ttf",
		"/usr/share/fonts/OTF/IBMPlexSans-Regular.otf",
	}},
	{"IBM Plex Mono", []string{
		"/usr/share/fonts/TTF/IBMPlexMono-Regular.ttf",
		"/usr/share/fonts/truetype/ibm-plex/IBMPlexMono-Regular.ttf",
		"/usr/share/fonts/OTF/IBMPlexMono-Regular.otf",
	}},
	{"Cascadia Code", []string{
		"/usr/share/fonts/TTF/CascadiaCode-Regular.ttf",
		"/usr/share/fonts/cascadia-code/CascadiaCode-Regular.ttf",
	}},
}

// Catalog returns the entries whose underlying font files exist on disk,
// always prefixed by a "Built-in (Go)" entry that maps to the empty path.
func Catalog() []Font {
	out := []Font{{Name: "Built-in (Go)", Path: ""}}
	for _, c := range candidates {
		for _, p := range c.paths {
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				out = append(out, Font{Name: c.name, Path: p})
				break
			}
		}
	}
	return out
}

// CatalogIncluding behaves like Catalog but ensures the given path is
// represented as an entry (named "Custom: <basename>") if it isn't already in
// the curated list. Empty path is a no-op.
func CatalogIncluding(path string) []Font {
	out := Catalog()
	if path == "" {
		return out
	}
	for _, f := range out {
		if f.Path == path {
			return out
		}
	}
	return append(out, Font{Name: "Custom: " + filepath.Base(path), Path: path})
}

// IndexOf returns the index of path in the catalog, or 0 (built-in) if it
// isn't present.
func IndexOf(catalog []Font, path string) int {
	for i, f := range catalog {
		if f.Path == path {
			return i
		}
	}
	return 0
}
