package cli

import "fmt"

func demoRun([]string) int {
	fmt.Println(boxed([]string{
		accent("◆") + " " + bold("ok — Universal Agent"),
		dim("One agent to rule them all. Desktop. Browser. Terminal. Voice."),
	}))
	fmt.Println()
	fmt.Printf("  %s %s\n", accent("▌"), bold("What OK can do for you:"))
	fmt.Println()

	demos := []struct{ icon, cmd, desc string }{
		{"🎙️", "/voice-chat", "Talk to OK — speech-to-speech conversation"},
		{"🖥️", `ok run "open notepad and write a poem"`, "Desktop automation (computer-use)"},
		{"🤖", "ok chat", "Interactive coding agent with tools"},
		{"🌐", `ok run "research latest AI news and summarize"`, "Web research + analysis"},
		{"🔧", "/model gemini-2.5-flash", "Switch models mid-conversation"},
		{"🏥", "ok doctor", "Diagnose your setup in 1 second"},
		{"📦", "ok update", "Self-update to the latest version"},
		{"🚀", "ok setup", "Configure providers interactively"},
		{"🎨", `ok run "create a web page"`, "Full-stack app in one command"},
	}
	for _, d := range demos {
		fmt.Printf("  %s  %s %s\n", d.icon, padRight(d.cmd, 40), dim(d.desc))
	}

	fmt.Println()
	fmt.Printf("  %s\n", accent("▌")+" "+bold("Quick start:"))
	fmt.Println()
	fmt.Printf("   1.  %s — %s\n", padRight("set DEEPSEEK_API_KEY=sk-...", 42), dim("set your API key"))
	fmt.Printf("   2.  %s — %s\n", padRight("ok chat", 42), dim("start chatting"))
	fmt.Printf("   3.  %s — %s\n", padRight(`try "/voice-chat"`, 42), dim("talk to OK with your voice"))
	fmt.Println()
	fmt.Printf("  %s\n", dim("No config file? OK auto-detects API keys from your environment."))
	fmt.Printf("  %s\n", dim("Just set an *_API_KEY and run `ok chat` — it Just Works."))
	fmt.Println()
	return 0
}
