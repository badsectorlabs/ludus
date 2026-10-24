# Third-Party Notices

This project includes the following third-party software.

{{/* go-licenses emits one row per license found in a library's license file; render each library once, listing all of them. */}}
{{ $prev := "" }}{{ range . }}{{ if ne .Name $prev }}{{ $name := .Name }}
## {{ .Name }}

* Version: {{ .Version }}
* License: [{{ $sep := "" }}{{ range $ }}{{ if eq .Name $name }}{{ $sep }}{{ .LicenseName }}{{ $sep = ", " }}{{ end }}{{ end }}]({{ .LicenseURL }})

```
{{ if ne .LicensePath "Unknown" }}{{ .LicenseText }}{{ end }}
```

{{ end }}{{ $prev = .Name }}{{ end }}
