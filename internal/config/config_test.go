package config

import "testing"

func TestEnv(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		fallback string
		want     string
	}{
		{name: "uses configured value", value: "127.0.0.1:5558", fallback: ":5558", want: "127.0.0.1:5558"},
		{name: "uses fallback when unset", fallback: "./data", want: "./data"},
		{name: "uses fallback when empty", value: "", fallback: "./data", want: "./data"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const name = "KAVEN_TEST_VALUE"
			t.Setenv(name, test.value)
			if got := Env(name, test.fallback); got != test.want {
				t.Fatalf("Env(%q, %q) = %q, want %q", name, test.fallback, got, test.want)
			}
		})
	}
}

func TestEnvBool(t *testing.T) {
	t.Setenv("KAVEN_TEST_BOOL", "true")
	value, err := EnvBool("KAVEN_TEST_BOOL", false)
	if err != nil || !value {
		t.Fatalf("EnvBool configured value = %v, %v", value, err)
	}

	t.Setenv("KAVEN_TEST_BOOL", "")
	value, err = EnvBool("KAVEN_TEST_BOOL", true)
	if err != nil || !value {
		t.Fatalf("EnvBool fallback = %v, %v", value, err)
	}

	t.Setenv("KAVEN_TEST_BOOL", "invalid")
	if _, err := EnvBool("KAVEN_TEST_BOOL", false); err == nil {
		t.Fatal("EnvBool accepted invalid value")
	}
}
