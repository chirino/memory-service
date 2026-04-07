//go:build !noopenai

package buildcaps

func init() {
	OpenAI = true
}
