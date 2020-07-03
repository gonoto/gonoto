# Go Noto

The goal of the Go Noto project is to produce easy-to-import packages that
provide access to embedded fonts with broad unicode coverage. This is similar
to the [gofont package](https://pkg.go.dev/golang.org/x/image/font/gofont)
that is part of the Go project's extended library, except using the
[Noto font family](https://www.google.com/get/noto/) created by Google.

Command `gonoto` generates all of the individual font repositories for the
Go Noto project.

## Disclaimer
This project is not affiliated with Google or the Go Project in any way. It is
a third-party project. Noto is a trademark of Google Inc. Noto fonts are
open-source and are released under the
[SIL Open Font License, Version 1.1](http://scripts.sil.org/cms/scripts/page.php?site_id=nrsi&id=OFL).

The code in this project merely packages the Noto fonts for redistribution.
The code to do that is released under the
[Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0).

## Using the Fonts
This project is focused on providing fonts with broad unicode coverage. If you
do not need this (e.g., if your program only displays English text), then you
should not use this project because it makes many tradeoffs that you do not
need to make. Instead, download a [Noto font](https://www.google.com/get/noto/)
and use the TTF/OTF file directly.

The fonts are packaged in OpenType Collection (OTC) format due to technical
limitations. To use the fonts, you must use a library that can read these
files, such as the
[sfnt package](https://pkg.go.dev/golang.org/x/image/font/sfnt) from the
extended Go library. `sfnt.Collection` will provide access to the individual
fonts contained in the collection. It is up to you to properly handle fallback
behavior: if a glyph is not found in font `i`, then check in font `i+1`.

There are multiple font packages available, each containing one font
collection with a given weight and style:

* [notosans](https://github.com/gonoto/notosans)
  * [notosansbold](https://github.com/gonoto/notosansbold)
  * [notosansbolditalic](https://github.com/gonoto/notosansbolditalic)
  * [notosansitalic](https://github.com/gonoto/notosansitalic)
  * [notosanscondensed](https://github.com/gonoto/notosanscondensed)
* [notoserif](https://github.com/gonoto/notoserif)
  * [notoserifbold](https://github.com/gonoto/notoserifbold)
  * [notoserifbolditalic](https://github.com/gonoto/notoserifbolditalic)
  * [notoserifitalic](https://github.com/gonoto/notoserifitalic)
  * [notoserifcondensed](https://github.com/gonoto/notoserifcondensed)
* [notomono](https://github.com/gonoto/notomono)
  * [notomonobold](https://github.com/gonoto/notomonobold)
  * [notomonobolditalic](https://github.com/gonoto/notomonobolditalic)
  * [notomonoitalic](https://github.com/gonoto/notomonoitalic)
  * [notomonocondensed](https://github.com/gonoto/notomonocondensed)

To access the font data, import the package of your choice and call the `OTC`
function that it provides. This will automatically embed the font data in your
binary and decompress the data on first use. The `OTC` function is safe for
concurrent use.

## What About Emoji? &#x1F63F;
Noto provides both black & white and color emoji files. However, the
[sfnt package](https://pkg.go.dev/golang.org/x/image/font/sfnt) does not
support color emoji (which are stored in the font as PNG images). Attempting
to use the color emoji font will return `sfnt.ErrColoredGlyph`. This is why
all of the fonts in this project embed the black & white emoji variants, which
do not require any special support.

## Generating the Repositories
To use this command to generate the font repositories, download the ZIP file
containing all Noto fonts from the
[Noto website](https://www.google.com/get/noto/) (`Noto-unhinted.zip`).
Compile and run the command with the path to the ZIP file as the first
argument and the output directory as the second argument.

## Design Philosophy
The Go Noto project aims to package fonts with the following goals, ordered
from most to least important:

1. Provide fonts from the Noto family with broad unicode coverage.
2. Make it easy for Go programs to import these fonts.
3. Embed the fonts in the final binary, with no need for external files.
4. Minimize the file size of the fonts in the final binary.
5. Minimize the file size of the Go packages.
6. Minimize the CPU and memory requirements of loading the fonts.
7. Provide a variety of font weights and styles.

Broad unicode coverage is not cheap, nor is it easy. Despite the amazing work
of the artists and programmers behind the Noto font family, there are many
technical limitations to overcome. Individual TTF/OTF font files, such as
those from the Noto family, cannot contain more than 2<sup>16</sup> glyphs
(letter shapes) due to limitations in the SFNT format. This is simply not
enough to cover all writing systems. This project overcomes the limitation by
merging Noto fonts for all languages into individual "OpenType Collection"
files in the OTC format. This means that the fonts can only be used by
OTC-aware applications that understand how to use these files. It also means
that the merged font files are very large&#x2014;around 100 MB. This large
file size drives many of the design decisions. 98% of this file size comes
from the Type 1 fonts embedded in the Chinese / Japanese / Korean language
files, so de-duplication of data beyond simple SFNT table de-duplication is
non-trivial.

Each font is placed in its own repository so that users are not required to
download all of the fonts. The fonts share very little data because larger
languages get more attention from the artists and thus the majority of glyphs
have unique outline data for each weight/style, so de-duplication within a
single large repository would not help much. In order to keep the final
binary size low, the Go compiler must store the font data contiguously in
native binary form. This can be accomplished by storing the data in a
`[]byte` literal, but there is another problem: there is no official way to
embed static assets in Go binaries (see
[Go issue 35950](https://github.com/golang/go/issues/35950)). This means that
the data must be stored in a Go source file. Writing it as a string or
`[]byte` literal multiplies the file size by 4 in the source file (because we
must write `\xFF` or `,255` to store a byte). Our embedding mechanism writes the
data as a `[]uint64` literal instead: the Go compiler still embeds the raw
data in the binary, and we have much less overhead in the source file (a
multiplier of 2 for writing the data in hex, plus another 16% expansion due to
having to write `,0x` after every 16 hex digits). We omit the spaces between
elements normally inserted by `gofmt`. In addition, the files are compressed
with gzip before being embedded. During embedding, the data is split into
multiple chunk files. This simplifies management of the git repository, is more
friendly to IDEs, and also enables progressive garbage collection during
decompression.

## Where are the Other Styles?
The Noto font family contains a wide range of styles, whereas only a few of
them are packaged by this project. This is mainly a result of the large file
size. Each additional style increases the final binary size by around 60 MB.
The available fonts have been limited to discourage embedding many variants.
Another reason is to avoid storing too much data on Github.