// Fixture-only packer template. Syntactically valid HCL so source-add can
// stage it under packer/, but missing builder details — running `packer build`
// against it is not the goal.

variable "vm_name" {
  type    = string
  default = "example-debian-12-template"
}

variable "iso_url" {
  type    = string
  default = "https://cdimage.debian.org/debian-cd/current/amd64/iso-cd/debian-12-netinst.iso"
}

variable "iso_checksum" {
  type    = string
  default = "none"
}

source "null" "placeholder" {
  communicator = "none"
}

build {
  name    = "example-debian-12-template"
  sources = ["source.null.placeholder"]
}
