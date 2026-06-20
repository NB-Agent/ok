import sys, os

path = r"C:\Users\Administrator\Desktop\daima\ok\desktop\frontend\src\styles.css"

with open(path, 'r', encoding='utf-8') as f:
    content = f.read()

# Split into lines, but preserve original line structure
lines = content.split('\n')

# Line 0 (index 0) is the minified portion
first_line = lines[0]

# Split at every '}' and join with '}\n'
# But we need to be careful: if the line already ends with '}', we don't want a trailing blank
parts = first_line.split('}')
new_first = '}\n'.join(parts)

# If the original didn't end with '}', the last part won't have '}' — correct.
# Actually split('}') removes the delimiter. So each part (except possibly last) needs '}' added back.
# Wait: "a}b}c".split('}') = ['a', 'b', 'c']
# Then '}\n'.join(['a', 'b', 'c']) = 'a}\nb}\nc'
# That's exactly what we want — each '}' is followed by newline, except the very last char if original ended with '}'.
# If original ended with '}', then last part is '' so we get '...c}\n' which has trailing newline. That's fine.

# Reconstruct: line 0 becomes new_first, keep everything from line 1 onward
new_lines = [new_first] + lines[1:]
new_content = '\n'.join(new_lines)

with open(path, 'w', encoding='utf-8') as f:
    f.write(new_content)

print("Done. Original lines:", len(lines), "New first line length:", len(new_first))
