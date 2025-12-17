// Package util is a set of utility variables or methods
package util

import mapset "github.com/deckarep/golang-set/v2"

var SupportedExt = mapset.NewSet(
	".jpeg", ".jpg", ".JPEG", ".JPG",
	".png", ".PNG",
)
