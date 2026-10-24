package ludusapi

import "testing"

// Router roles run only from the generated user-defined-roles playbook, so a
// config whose only roles are on the router must still generate it.
func TestHasUserDefinedRoles(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   bool
	}{
		{
			name:   "no roles",
			config: "ludus:\n  - vm_name: a\nrouter:\n  vm_name: r\n",
			want:   false,
		},
		{
			name:   "empty role lists",
			config: "ludus:\n  - vm_name: a\n    roles: []\nrouter:\n  roles: []\n",
			want:   false,
		},
		{
			name:   "VM roles only",
			config: "ludus:\n  - vm_name: a\n    roles:\n      - x\n",
			want:   true,
		},
		{
			name:   "router string role only",
			config: "ludus:\n  - vm_name: a\nrouter:\n  roles:\n    - x\n",
			want:   true,
		},
		{
			name:   "router dict role only",
			config: "ludus:\n  - vm_name: a\nrouter:\n  roles:\n    - name: x\n      depends_on:\n        - vm_name: a\n          role: y\n",
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasUserDefinedRoles([]byte(tt.config)); got != tt.want {
				t.Fatalf("hasUserDefinedRoles() = %v, want %v", got, tt.want)
			}
		})
	}
}
