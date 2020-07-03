// Command `gonoto` generates all of the individual font repositories for the Go Noto project.
package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Nik-U/otcmerge"
	"golang.org/x/sync/errgroup"
)

const modulePrefix = "github.com/gonoto/"
const moduleGoVersion = "1.14"

type fontDesc struct {
	filename string
	weight   int
	hDensity int
	vDensity int
	style    int
}

func main() {
	if len(os.Args) != 3 {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: %s INPUTZIP OUTPUTDIR", os.Args[0])
		os.Exit(1)
	}
	if err := generateFonts(os.Args[1], os.Args[2]); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Fatal error: %s\n", err.Error())
		os.Exit(1)
	}
}

func generateFonts(sourcePath string, outputDir string) error {
	z, err := zip.OpenReader(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to load Noto input ZIP: %w", err)
	}
	defer func() { _ = z.Close() }()
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// There is some confusion over whether SerifDisplay / SansDisplay are meant to be the compact or non-compact
	// versions of Serif / Sans. https://github.com/googlefonts/noto-source/blob/master/FONT_CONTRIBUTION.md seems to
	// suggest that Serif / Sans are "UI" fonts and that the "Display" variants are "less compact", which seems to
	// contradict the name. Moreover, comparing the versions with notodiff reveals that "Display" is actually more
	// compact (see https://github.com/googlefonts/noto-fonts/issues/1056 ). Consequently, we just ignore these variants
	// for now and do not generate any outputs based on them.
	families := []string{
		"SerifDisplay", "SansDisplay",
		"SansMono", "Serif", "Sans", "Mono",
		"Emoji", "KufiArabic", "NaskhArabic", "NastaliqUrdu"}
	weights := []string{"Thin", "ExtraLight", "Light", "DemiLight", "Regular", "Medium", "SemiBold", "Bold", "ExtraBold", "Black"}
	hDensities := []string{"ExtraCondensed", "Condensed", "SemiCondensed", ""}
	vDensities := []string{"UI", ""}
	styles := []string{"", "Italic"}

	type outputFamily struct {
		name        string // The name / subdirectory of the family to output
		inputFamily string // The family to import language glyphs from by default

		weight   string
		hDensity string
		vDensity string
		style    string

		prependComboFamilies []string // The default languages in these families are injected after default language
		appendComboFamilies  []string // The default languages in these families are injected after input languages

		description string // The package description
	}
	emoji := []string{"Emoji"}
	comboFamilies := []string{"KufiArabic", "NaskhArabic", "NastaliqUrdu"}
	outputFamilies := []outputFamily{
		{"notosans", "Sans", "Regular", "", "", "", emoji, comboFamilies, "provides the \"Noto Sans\" font collection. It is a proportional-width, sans-serif font."},
		{"notosansbold", "Sans", "Bold", "", "", "", emoji, comboFamilies, "provides the \"Noto Sans Bold\" font collection. It is a proportional-width, sans-serif font."},
		{"notosansbolditalic", "Sans", "Bold", "", "", "Italic", emoji, comboFamilies, "provides the \"Noto Sans Bold Italic\" font collection. It is a proportional-width, sans-serif font."},
		{"notosansitalic", "Sans", "Regular", "", "", "Italic", emoji, comboFamilies, "provides the \"Noto Sans Italic\" font collection. It is a proportional-width, sans-serif font."},
		{"notosanscondensed", "Sans", "Regular", "Condensed", "UI", "", emoji, comboFamilies, "provides the \"Noto Sans Condensed\" font collection. It is a proportional-width, sans-serif font."},

		{"notoserif", "Serif", "Regular", "", "", "", emoji, comboFamilies, "provides the \"Noto Serif\" font collection. It is a proportional-width, serif font."},
		{"notoserifbold", "Serif", "Bold", "", "", "", emoji, comboFamilies, "provides the \"Noto Serif Bold\" font collection. It is a proportional-width, serif font."},
		{"notoserifbolditalic", "Serif", "Bold", "", "", "Italic", emoji, comboFamilies, "provides the \"Noto Serif Bold Italic\" font collection. It is a proportional-width, serif font."},
		{"notoserifitalic", "Serif", "Regular", "", "", "Italic", emoji, comboFamilies, "provides the \"Noto Serif Italic\" font collection. It is a proportional-width, serif font."},
		{"notoserifcondensed", "Serif", "Regular", "Condensed", "UI", "", emoji, comboFamilies, "provides the \"Noto Serif Condensed\" font collection. It is a proportional-width, serif font."},

		{"notomono", "SansMono", "Regular", "", "", "", emoji, nil, "provides the \"Noto Mono\" font collection. It is a fixed-width, serif font."},
		{"notomonobold", "SansMono", "Bold", "", "", "", emoji, nil, "provides the \"Noto Mono Bold\" font collection. It is a fixed-width, serif font."},
		{"notomonobolditalic", "SansMono", "Bold", "", "", "Italic", emoji, nil, "provides the \"Noto Mono Bold Italic\" font collection. It is a fixed-width, serif font."},
		{"notomonoitalic", "SansMono", "Regular", "", "", "Italic", emoji, nil, "provides the \"Noto Mono Italic\" font collection. It is a fixed-width, serif font."},
		{"notomonocondensed", "SansMono", "Regular", "Condensed", "UI", "", emoji, nil, "provides the \"Noto Mono Condensed\" font collection. It is a fixed-width, serif font."},
	}

	fontDescriptions := make(map[string]map[string][]*fontDesc)
	for _, f := range families {
		fontDescriptions[f] = make(map[string][]*fontDesc)
	}
	languageSet := make(map[string]struct{})

	var dataLock sync.Mutex
	fontData := make(map[string][]byte)

	eg := new(errgroup.Group)
	for _, f := range z.File {
		func(f *zip.File) {
			eg.Go(func() error {
				ext := filepath.Ext(f.Name)
				if len(f.Name) < 9 {
					return nil
				}
				if ext != ".otf" && ext != ".ttf" {
					return nil
				}
				if f.Name[:4] != "Noto" {
					return nil
				}
				name := f.Name[4 : len(f.Name)-len(ext)]

				terms := strings.SplitN(name, "-", 2)
				if len(terms) != 2 {
					return nil
				}
				domain := terms[0]
				styling := terms[1]

				family, domain, familyName := indexOf(domain, families, true)
				if family < 0 {
					return nil
				}

				vDensity, domain, _ := indexOf(domain, vDensities, false)
				language := domain
				style, styling, _ := indexOf(styling, styles, false)
				hDensity, styling, _ := indexOf(styling, hDensities, true)
				weight, styling, _ := indexOf(styling, weights, true)

				// If no explicit weight was found, assume it was "Regular"
				if weight < 0 {
					weight = exactIndexOf("Regular", weights)
				}

				if styling != "" {
					return nil
				}
				d := &fontDesc{
					filename: f.Name,
					weight:   weight,
					hDensity: hDensity,
					vDensity: vDensity,
					style:    style,
				}

				fmt.Printf("Loading source font %s\n", f.Name)

				r, err := f.Open()
				if err != nil {
					return err
				}
				data := make([]byte, f.UncompressedSize64)
				_, err = io.ReadFull(r, data)
				_ = r.Close()
				if err != nil {
					return err
				}
				dataLock.Lock()
				defer dataLock.Unlock()
				fontDescriptions[familyName][language] = append(fontDescriptions[familyName][language], d)
				languageSet[language] = struct{}{}
				fontData[f.Name] = data

				return nil
			})
		}(f)
	}
	if err := eg.Wait(); err != nil {
		return fmt.Errorf("failed to read a font file from the Noto input ZIP: %w", err)
	}
	_ = z.Close()

	languages := make([]string, 0, len(languageSet))
	for l := range languageSet {
		languages = append(languages, l)
	}
	sort.Strings(languages) // Notably, this means that CJKsc takes priority over CJKtc for shared Han glyphs

	availableBufs := make(chan *seekBuffer)
	recycleBufs := make(chan *seekBuffer)
	go func() {
		bufs := make([]*seekBuffer, runtime.NumCPU())
		for i := range bufs {
			bufs[i] = &seekBuffer{buf: make([]byte, 4096)}
		}
		for {
			var outBuf *seekBuffer
			var outChan chan *seekBuffer
			if len(bufs) > 0 {
				outBuf = bufs[len(bufs)-1]
				outChan = availableBufs
			}
			select {
			case outChan <- outBuf:
				bufs = bufs[:len(bufs)-1]
			case b := <-recycleBufs:
				bufs = append(bufs, b)
			}
		}
	}()

	eg = new(errgroup.Group)
	for _, outFamily := range outputFamilies {
		func(outFamily outputFamily) {
			eg.Go(func() error {
				weight := exactIndexOf(outFamily.weight, weights)
				hDensity := exactIndexOf(outFamily.hDensity, hDensities)
				vDensity := exactIndexOf(outFamily.vDensity, vDensities)
				style := exactIndexOf(outFamily.style, styles)

				var sourceFonts []*fontDesc
				// Roughly organize fonts from most likely to least likely: ASCII, then combo families
				// (e.g., Emoji), then all other languages sorted alphabetically.
				sourceFonts = appendMatchingFonts(sourceFonts, fontDescriptions[outFamily.inputFamily][""], weight, hDensity, vDensity, style)
				for _, comboFamily := range outFamily.prependComboFamilies {
					sourceFonts = appendMatchingFonts(sourceFonts, fontDescriptions[comboFamily][""], weight, hDensity, vDensity, style)
				}
				for _, l := range languages {
					if l == "" {
						continue
					}
					sourceFonts = appendMatchingFonts(sourceFonts, fontDescriptions[outFamily.inputFamily][l], weight, hDensity, vDensity, style)
				}
				for _, comboFamily := range outFamily.appendComboFamilies {
					sourceFonts = appendMatchingFonts(sourceFonts, fontDescriptions[comboFamily][""], weight, hDensity, vDensity, style)
				}

				buf := <-availableBufs
				defer func() { recycleBufs <- buf }()
				if err := generateFont(outFamily.name, outFamily.description, filepath.Join(outputDir, outFamily.name), sourceFonts, fontData, buf); err != nil {
					return err
				}
				return nil
			})
		}(outFamily)
	}
	if err := eg.Wait(); err != nil {
		return fmt.Errorf("error while outputting merged fonts: %w", err)
	}
	return nil
}

func indexOf(s string, l []string, prefix bool) (int, string, string) {
	def := -1
	for i, x := range l {
		if x == "" {
			def = i
			continue
		}
		if prefix {
			if strings.HasPrefix(s, x) {
				return i, s[len(x):], x
			}
		} else {
			if strings.HasSuffix(s, x) {
				return i, s[:len(s)-len(x)], x
			}
		}
	}
	return def, s, ""
}

func exactIndexOf(s string, l []string) int {
	for i, x := range l {
		if x == s {
			return i
		}
	}
	return -1
}

func appendMatchingFonts(out []*fontDesc, descriptions []*fontDesc, weight int, hDensity int, vDensity int, style int) []*fontDesc {
	var match *fontDesc
	var matchDist int64
	descDistance := func(d *fontDesc) int64 {
		weightDist := weight - d.weight
		if weightDist < 0 {
			weightDist = -weightDist
		}
		hDensityDist := hDensity - d.hDensity
		if hDensityDist < 0 {
			hDensityDist = -hDensityDist
		}
		vDensityDist := vDensity - d.vDensity
		if vDensityDist < 0 {
			vDensityDist = -vDensityDist
		}
		styleDist := style - d.style
		if styleDist < 0 {
			styleDist = -styleDist
		}
		// Produce a weighted distance measure imposing a strict priority of features. This is a bit arbitrary but
		// decouples this function from explicit knowledge of the exact feature sets. For ties, always prefer the larger
		// index to make the result deterministic.
		return 1e14*int64(styleDist) + 1e12*int64(weightDist) + 1e10*int64(hDensityDist) + 1e8*int64(vDensityDist) -
			1e6*int64(d.style) - 1e4*int64(d.weight) - 1e2*int64(d.hDensity) - int64(d.vDensity)
	}
	for _, d := range descriptions {
		dist := descDistance(d)
		if match == nil || dist < matchDist {
			match = d
			matchDist = dist
		}
	}
	if match != nil {
		out = append(out, match)
	}
	return out
}

func generateFont(packageName string, description string, outputDir string, sourceFonts []*fontDesc, fontData map[string][]byte, buf *seekBuffer) error {
	fmt.Printf("Generating merged font %s\n", outputDir)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create font directory %s: %w", outputDir, err)
	}

	inputs := make([]io.ReadSeeker, len(sourceFonts))
	for i, f := range sourceFonts {
		inputs[i] = bytes.NewReader(fontData[f.filename])
	}

	buf.Reset()
	if err := otcmerge.Merge(inputs, buf); err != nil {
		return err
	}

	if err := generateSupportFiles(packageName, description, outputDir); err != nil {
		return err
	}
	if err := generateChunks(packageName, outputDir, buf.buf); err != nil {
		return err
	}
	return nil
}

func generateSupportFiles(packageName string, description string, outputDir string) error {
	if err := ioutil.WriteFile(filepath.Join(outputDir, "otc.go"),
		[]byte(`// Copyright 2020 Go Noto Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// Noto is a trademark of Google Inc. Noto fonts are open source.
// All Noto fonts are published under the SIL Open Font License, Version 1.1.

// package `+packageName+` `+description+`
// This font collection provides broad unicode coverage.
// Special software is required to use OpenType font collections.
//
// See https://github.com/gonoto/gonoto for details.
package `+packageName+`

import (
	"compress/gzip"
	"io"
	"sync"
)

type chunkDecoder struct{}

func (d chunkDecoder) Read(p []byte) (n int, err error) {
	for len(p) >= 8 {
		if len(chunks) < 1 {
			return n, io.EOF
		}
		if len(chunks[0]) < 1 {
			chunks = chunks[1:]
			continue
		}
		u := chunks[0][0]
		chunks[0] = chunks[0][1:]
		p[0] = byte(u & 0xff)
		p[1] = byte(u & 0xff00 >> 8)
		p[2] = byte(u & 0xff0000 >> 16)
		p[3] = byte(u & 0xff000000 >> 24)
		p[4] = byte(u & 0xff00000000 >> 32)
		p[5] = byte(u & 0xff0000000000 >> 40)
		p[6] = byte(u & 0xff000000000000 >> 48)
		p[7] = byte(u & 0xff00000000000000 >> 56)
		p = p[8:]
		n += 8
	}
	return n, nil
}

var initOnce sync.Once
var otcData []byte

// OTC returns the font data as an OpenType collection.
func OTC() []byte {
	initOnce.Do(func() {
		var cr chunkDecoder
		otcData = make([]byte, decompressedSize)
		r, _ := gzip.NewReader(cr)
		_, _ = io.ReadFull(r, otcData)
		chunks = nil
	})
	return otcData
}
`), 0644); err != nil {
		return fmt.Errorf("failed to write decoder file: %w", err)
	}
	if err := ioutil.WriteFile(filepath.Join(outputDir, "README.md"), []byte(`# Go Noto

Package `+packageName+` `+description+`
This font collection provides broad unicode coverage.
Special software is required to use OpenType font collections.

This font package is part of the Go Noto project.
For usage information, see https://github.com/gonoto/gonoto

## License
Noto is a trademark of Google Inc. Noto fonts are open source.
All Noto fonts are published under the SIL Open Font License, Version 1.1.

This package contains additional code for the purpose of redistributing Noto fonts.
This additional code is licensed under the Apache License, Version 2.0.
`), 0644); err != nil {
		return fmt.Errorf("failed to write README file: %w", err)
	}
	if err := ioutil.WriteFile(filepath.Join(outputDir, "go.mod"), []byte("module "+modulePrefix+packageName+"\n\ngo "+moduleGoVersion+"\n"), 0644); err != nil {
		return fmt.Errorf("failed to write go.mod file: %w", err)
	}
	if err := ioutil.WriteFile(filepath.Join(outputDir, "LICENSE"), []byte(repoLicense), 0644); err != nil {
		return fmt.Errorf("failed to write LICENSE file: %w", err)
	}
	return nil
}

func generateChunks(packageName string, outputDir string, data []byte) error {
	const chunkSize = 20 * 1024 * 1024

	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
		gz, err := gzip.NewWriterLevel(pw, gzip.BestCompression)
		if err != nil {
			return
		}
		if _, err := gz.Write(data); err != nil {
			return
		}
		if err := gz.Close(); err != nil {
			return
		}
	}()

	var chunkVars []string
	for i := 0; ; i++ {
		r := io.LimitReader(pr, chunkSize)
		chunkVar := fmt.Sprintf("chunk%d", i)
		more, err := writeChunk(packageName, filepath.Join(outputDir, fmt.Sprintf("chunk%d.go", i)), chunkVar, r)
		if err != nil {
			return fmt.Errorf("failed to write data chunk %d for font %s: %w", i, outputDir, err)
		}
		if !more {
			break
		}
		chunkVars = append(chunkVars, chunkVar)
	}
	if err := ioutil.WriteFile(filepath.Join(outputDir, "chunk.go"),
		[]byte("package "+packageName+"\n\n"+
			"var chunks = [][]uint64{"+strings.Join(chunkVars, ", ")+"}\n"+
			"const decompressedSize = "+strconv.Itoa(len(data))+"\n"),
		0644); err != nil {
		return fmt.Errorf("failed to write chunk file: %w", err)
	}
	return nil
}

func writeChunk(packageName string, outputFile string, varName string, r io.Reader) (bool, error) {
	fw, err := os.Create(outputFile)
	if err != nil {
		return false, err
	}
	defer func() { _ = fw.Close() }()
	w := bufio.NewWriter(fw)

	var buf [4096]byte
	if _, err := w.WriteString(
		"// Noto is a trademark of Google Inc. Noto fonts are open source.\n" +
			"// All Noto fonts are published under the SIL Open Font License, Version 1.1.\n\n" +
			"package " + packageName + "\n\n" +
			"var " + varName + " = []uint64{"); err != nil {
		return false, err
	}
	empty := false
	comma := false
	for {
		n, err := io.ReadFull(r, buf[:])
		if n%8 != 0 {
			copy(buf[n:], []byte{0, 0, 0, 0})
			n += 8 - n%8
		}
		for i := 0; i < n; i += 8 {
			encoded := binary.LittleEndian.Uint64(buf[i : i+8])
			if comma {
				if _, err := w.WriteString(","); err != nil {
					return false, err
				}
			}
			comma = true
			if _, err := fmt.Fprintf(w, "0x%02X", encoded); err != nil {
				return false, err
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) && !comma {
				empty = true
			}
			break
		}
	}
	if _, err := w.WriteString("}\n"); err != nil {
		return false, err
	}
	if err := w.Flush(); err != nil {
		return false, err
	}
	if err := fw.Close(); err != nil {
		return false, err
	}
	if empty {
		if err := os.Remove(outputFile); err != nil {
			return false, fmt.Errorf("failed to delete superfluous chunk file %s: %w", outputFile, err)
		}
		return false, nil
	}
	return true, nil
}
