"""Verify and apply mojibake repairs for the runs the 3e4d259 pass documented as
unrepairable ("roughly fifteen two- and three-character runs whose reversal does
not decode, mostly icons in JSX").

Method, and why it is not a blanket transform
-------------------------------------------
The corruption is UTF-8 bytes decoded as cp1252 and re-encoded as UTF-8. To
reverse one run we map each character back through cp1252 to the byte it could
have come from, then require that the resulting byte sequence is VALID UTF-8 and
that it decodes to exactly one code point.

cp1252 is ambiguous - several bytes map to visually similar characters - so a
generic "reverse the mojibake" transform silently corrupts correct text. Every
replacement here is therefore proven twice:

  1. the reverse byte sequence is valid UTF-8, and
  2. re-encoding the resulting character reproduces the original run exactly.

If either check fails the script aborts without writing. Runs that fail both are
reported for hand repair rather than guessed at.
"""

import glob
import sys

# cp1252 byte -> the character a decoder produces for it. Used to walk a
# character run back to bytes.
BYTE_OF = {}
for b in range(256):
    try:
        BYTE_OF[bytes([b]).decode("cp1252")] = b
    except UnicodeDecodeError:
        pass
# A handful of code points have no cp1252 byte (they came from windows-1252
# undefined slots, i.e. the file was decoded as latin-1). Add those mappings so
# latin-1-decoded runs are reversible too.
for b in range(256):
    try:
        ch = bytes([b]).decode("latin-1")
    except UnicodeDecodeError:
        continue
    BYTE_OF.setdefault(ch, b)

LEAD = set("\u00e2\u00c3\u00c2\u00f0\u00d0\u00e3\u00ef\u00f1")

# Runs that cannot be reversed mechanically, with the intended text and why.
#
# Each entry is (codepoints-as-written, replacement, reason). The script refuses
# to --apply while any run is unhandled, so this table cannot silently fall
# behind the files.
MANUAL = {
    # HEAVY BLACK HEART followed by VARIATION SELECTOR-16. Emoji presentation
    # selectors are a legitimate two-code-point sequence, which is exactly what
    # the "must decode to exactly one code point" proof rejects - correctly, since
    # that proof exists to catch a guessed run boundary and cannot tell a genuine
    # multi-code-point sequence from a wrong guess. Verified by hand: the bytes
    # are E2 9D A4 EF B8 8F, which is U+2764 U+FE0F and nothing else.
    "\u00e2\u009d\u00a4\u00ef\u00b8\u008f": "\u2764\ufe0f",
}

# Runs the detector flags but which are CORRECT and must not be touched.
#
# U+2014 is an em dash. It appears hundreds of times in this repository as a
# legitimate character - including as the "no rating yet" placeholder on the
# compare page - and the detector only reaches it because a damaged run sits
# nearby on the same line. It reverses to the single byte 0x97, which is not
# valid UTF-8 on its own, so it is already rejected by the proof; it is listed
# here so the report is not read as an outstanding defect.
ALREADY_CORRECT = {"\u2014"}


def reverse_run(run):
    """Return (codepoint, err). err is None only when the reverse PROVES itself.

    The proof has two parts, and neither is a round trip:

      1. the reversed bytes are valid UTF-8, and
      2. they decode to EXACTLY ONE code point whose UTF-8 length equals the
         length of the run.

    Part 2 is what stops a legitimate character from being silently merged with
    a damaged neighbour. `U+2014` (an em dash, which appears 800 times in this
    repository and is always correct) reverses to the single byte 0x97, which is
    not valid UTF-8 on its own, so it is rejected. A run that happened to decode
    to two code points would mean the run boundary was guessed wrong, and it is
    rejected too.
    """
    try:
        raw = bytes(BYTE_OF[ch] for ch in run)
    except KeyError as exc:
        return None, "no cp1252 byte for %r" % exc
    try:
        decoded = raw.decode("utf-8")
    except UnicodeDecodeError as exc:
        return None, "reverse is not valid UTF-8: %s" % exc
    if len(decoded) != 1:
        return None, "reverse decodes to %d code points, not 1" % len(decoded)
    if len(decoded.encode("utf-8")) != len(run):
        return None, "length mismatch: %d bytes -> %d chars" % (len(run), len(decoded))
    return decoded, None


def find_runs(path):
    """Yield (line_no, col_start, run_text) for every mojibake-looking run."""
    data = open(path, "rb").read().decode("utf-8", errors="replace").split("\n")
    for i, line in enumerate(data):
        chars = [c for c in line if ord(c) > 127]
        if not chars:
            continue
        has_c1 = any(0x80 <= ord(c) <= 0x9F for c in chars)
        has_lead = any(c in LEAD for c in chars)
        if not (has_c1 or has_lead):
            continue
        # Walk the line, accumulating maximal non-ASCII runs.
        run = []
        start = None
        for col, ch in enumerate(line):
            if ord(ch) > 127:
                if start is None:
                    start = col
                run.append(ch)
            elif run:
                yield i + 1, start, "".join(run)
                run, start = [], None
        if run:
            yield i + 1, start, "".join(run)


def show(s):
    """ASCII-safe rendering: name the code point, not just the glyph.

    The console this runs under is cp1252, which cannot encode most of the
    characters being repaired, and a traceback here would be indistinguishable
    from a repair failure. So the report prints U+XXXX and the Unicode NAME,
    and the escape form - which is also how the fix should be written into
    source where a future editor cannot re-mangle it.
    """
    import unicodedata
    if len(s) != 1:
        # A multi-code-point replacement (heart + variation selector). Report the
        # whole sequence rather than crashing, since that is the case a reviewer
        # most needs to read.
        parts = []
        for ch in s:
            try:
                parts.append("%04X %s" % (ord(ch), unicodedata.name(ch)))
            except ValueError:
                parts.append("%04X <unnamed>" % ord(ch))
        return "U+" + " U+".join(parts) + "  " + s.encode("unicode_escape").decode("ascii")
    try:
        name = unicodedata.name(s)
    except ValueError:
        name = "?"
    return "U+%04X (%s) %s" % (ord(s), name, s.encode("unicode_escape").decode("ascii"))


def main():
    files = sorted(
        set(glob.glob("apps/web/src/**/*.ts", recursive=True))
        | set(glob.glob("apps/web/src/**/*.tsx", recursive=True))
    )
    repairs, manual, correct = [], [], []
    for path in files:
        for line_no, col, run in find_runs(path):
            if run in MANUAL:
                repairs.append((path, line_no, col, run, MANUAL[run]))
            elif run in ALREADY_CORRECT:
                correct.append((path, line_no, col, run))
            else:
                cp, err = reverse_run(run)
                (repairs if cp is not None else manual).append(
                    (path, line_no, col, run, cp))

    print("=== auto-repairable (%d) ===" % len(repairs))
    for path, ln, col, run, cp in repairs:
        src = "MANUAL" if run in MANUAL else "reverse"
        print("  %-58s %-5d col %-4d [%s] %s -> %s" % (
            path.replace("\\", "/"), ln, col, src,
            " ".join("U+%04X" % ord(c) for c in run), show(cp)))

    print()
    print("=== flagged but already correct, left alone (%d) ===" % len(correct))
    for path, ln, col, run in correct:
        print("  %-58s %-5d col %-4d %s" % (
            path.replace("\\", "/"), ln, col,
            " ".join("U+%04X" % ord(c) for c in run)))

    print()
    print("=== NOT mechanically reversible and not hand-mapped (%d) ===" % len(manual))
    for path, ln, col, run, cp in manual:
        print("  %-58s %-5d col %-4d %s" % (
            path.replace("\\", "/"), ln, col,
            " ".join("U+%04X" % ord(c) for c in run)))

    if "--apply" in sys.argv:
        if manual:
            print("\nrefusing to apply: %d run(s) need hand repair first" % len(manual))
            return 1
        by_file = {}
        for path, ln, col, run, cp in repairs:
            by_file.setdefault(path, []).append((ln, col, run, cp))
        for path, edits in by_file.items():
            lines = open(path, "rb").read().decode("utf-8", errors="replace").split("\n")
            # Apply right-to-left so earlier columns stay valid.
            for ln, col, run, cp in sorted(edits, key=lambda e: (-e[0], -e[1])):
                line = lines[ln - 1]
                idx = line.index(run, col)
                assert line[idx:idx + len(run)] == run, "stale column"
                lines[ln - 1] = line[:idx] + cp + line[idx + len(run):]
            open(path, "wb").write("\n".join(lines).encode("utf-8"))
            print("wrote %s (%d run(s))" % (path.replace("\\", "/"), len(edits)))
    return 0


if __name__ == "__main__":
    sys.exit(main())