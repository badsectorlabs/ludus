package protocol

import (
	"strings"
	"testing"
)

func TestVMActionIconValidation(t *testing.T) {
	for name, want := range map[string]bool{
		"Terminal": true, "Screen": true, "Terminal_3270": true, "4K": true, "ViewMode_2": true,
		"": false, "../Icon": false, "Icon.js": false, "Icon-2zUDcllO": false, "Terminal<script>": false, strings.Repeat("A", 65): false,
	} {
		if got := ValidVMActionIconName(name); got != want {
			t.Errorf("ValidVMActionIconName(%q) = %v, want %v", name, got, want)
		}
	}
	for color, want := range map[string]bool{
		"#c6a153": true, "#FFF": true, "#c6a15": false, "c6a153": false, "red": false, "#c6a153;x:y": false,
	} {
		if got := ValidVMActionColor(color); got != want {
			t.Errorf("ValidVMActionColor(%q) = %v, want %v", color, got, want)
		}
	}
	svg := `<svg viewBox="0 0 16 16"><path d="M0 0h16v16H0z"/></svg>`
	for icon, want := range map[string]bool{
		svg: true,
		"<?xml version=\"1.0\"?><!-- Generator: x -->\n" + svg + "\n<!-- trailing -->\n": true,
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"/>`:                    true,
		"<svgx></svgx>":                          false,
		"<div>" + svg + "</div>":                 false,
		"<svg></svg><script>alert(1)</script>":   false,
		"<svg><path></svg>":                      false,
		`<!DOCTYPE svg [<!ENTITY x "y">]>` + svg: false,
		"text " + svg:                            false,
		"":                                       false,
		"<svg>" + strings.Repeat(" ", MaxVMActionIconSVG) + "</svg>": false,
	} {
		if got := ValidVMActionIconSVG(icon); got != want {
			t.Errorf("ValidVMActionIconSVG(%.40q) = %v, want %v", icon, got, want)
		}
	}
}
