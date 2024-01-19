# VHS documentation
#
# Output:
#   Output <path>.gif               Create a GIF output at the given <path>
#   Output <path>.mp4               Create an MP4 output at the given <path>
#   Output <path>.webm              Create a WebM output at the given <path>
#
# Require:
#   Require <string>                Ensure a program is on the $PATH to proceed
#
# Settings:
#   Set FontSize <number>           Set the font size of the terminal
#   Set FontFamily <string>         Set the font family of the terminal
#   Set Height <number>             Set the height of the terminal
#   Set Width <number>              Set the width of the terminal
#   Set LetterSpacing <float>       Set the font letter spacing (tracking)
#   Set LineHeight <float>          Set the font line height
#   Set LoopOffset <float>%         Set the starting frame offset for the GIF loop
#   Set Theme <json|string>         Set the theme of the terminal
#   Set Padding <number>            Set the padding of the terminal
#   Set Framerate <number>          Set the framerate of the recording
#   Set PlaybackSpeed <float>       Set the playback speed of the recording
#   Set MarginFill <file|#000000>   Set the file or color the margin will be filled with.
#   Set Margin <number>             Set the size of the margin. Has no effect if MarginFill isn't set.
#   Set BorderRadius <number>       Set terminal border radius, in pixels.
#   Set WindowBar <string>          Set window bar type. (one of: Rings, RingsRight, Colorful, ColorfulRight)
#   Set WindowBarSize <number>      Set window bar size, in pixels. Default is 40.
#   Set TypingSpeed <time>          Set the typing speed of the terminal. Default is 50ms.
#
# Sleep:
#   Sleep <time>                    Sleep for a set amount of <time> in seconds
#
# Type:
#   Type[@<time>] "<characters>"    Type <characters> into the terminal with a
#                                   <time> delay between each character
#
# Keys:
#   Escape[@<time>] [number]        Press the Escape key
#   Backspace[@<time>] [number]     Press the Backspace key
#   Delete[@<time>] [number]        Press the Delete key
#   Insert[@<time>] [number]        Press the Insert key
#   Down[@<time>] [number]          Press the Down key
#   Enter[@<time>] [number]         Press the Enter key
#   Space[@<time>] [number]         Press the Space key
#   Tab[@<time>] [number]           Press the Tab key
#   Left[@<time>] [number]          Press the Left Arrow key
#   Right[@<time>] [number]         Press the Right Arrow key
#   Up[@<time>] [number]            Press the Up Arrow key
#   Down[@<time>] [number]          Press the Down Arrow key
#   PageUp[@<time>] [number]        Press the Page Up key
#   PageDown[@<time>] [number]      Press the Page Down key
#   Ctrl+<key>                      Press the Control key + <key> (e.g. Ctrl+C)
#
# Display:
#   Hide                            Hide the subsequent commands from the output
#   Show                            Show the subsequent commands in the output

Output static/img/demo.mp4

Set WindowBar Colorful

Require echo
Require ludus

Set Shell "zsh"
Set FontSize 20
Set Width 1600
Set Height 900

Type "echo 'Welcome to Ludus!'"
Sleep 200ms
Enter
Sleep 2s

Type@50ms "echo 'I already have a Ludus server installed and range deployed'"
Sleep 500ms
Enter

Type "ludus range list"
Sleep 100ms
Enter
Sleep 5s

Type@50ms "echo 'Lets check out the config that created this range'"
Sleep 500ms
Enter

Type "ludus range config get | less"
Sleep 500ms
Enter
Sleep 7s
PageDown
Sleep 6s
Type "q"

Sleep 1s

Type@50ms "echo 'We can easily snapshot the Windows VMs and block them from the internet for testing'"
Sleep 500ms
Enter

Type "ludus testing start"
Sleep 500ms
Enter

Sleep 25s

Type "ludus testing status"
Sleep 100ms
Enter
Sleep 5s

Type@50ms "echo 'Perhaps we want to allow our VMs under test to reach out to example.com'"
Sleep 500ms
Enter

Type "ludus testing allow -d example.com"
Sleep 100ms
Enter
Sleep 10s

Type "ludus testing status"
Sleep 100ms
Enter
Sleep 5s

Type@50ms "echo 'We exit testing (revert to snapshot and unblock) just as easily'"
Sleep 500ms
Enter

Type "ludus testing stop"
Sleep 500ms
Enter

Sleep 25s

Type "ludus testing status"
Sleep 100ms
Enter
Sleep 5s


# Type "ludus apikey"
# Enter
# Paste
# Enter

