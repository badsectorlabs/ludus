// Fixture-only Packer template for CI `templates add` tests. Syntactically
// valid HCL with a discoverable "*-template" name so it stages under the
// caller's packer dir; it is NOT buildable (null source) — running
// `packer build` against it is not the goal. The real template library now
// ships via the ludus-source-bsl source
// (https://github.com/badsectorlabs/ludus-source-bsl).

variable "vm_name" {
  type    = string
  default = "ci-add-test-template"
}

source "null" "placeholder" {
  communicator = "none"
}

build {
  name    = "ci-add-test-template"
  sources = ["source.null.placeholder"]
}
