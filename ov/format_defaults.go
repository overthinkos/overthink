package main

import _ "embed"

//go:embed defaults/distro.yml
var defaultDistroYAML []byte

//go:embed defaults/build.yml
var defaultBuildYAML []byte

//go:embed defaults/builder.yml
var defaultBuilderYAML []byte
