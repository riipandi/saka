# Reference card — the human-docs tone (from `create-readme`)

The `docs/` tree is written for a reader who wants to use the product, not review the code. The tone rules the good READMEs follow, applied to every human-facing document.

## The rules

- **Lead with what the reader came for.** The first paragraph answers "what is this and why do I care"; the how comes after the why. No throat-clearing, no history of the feature.
- **One idea per paragraph.** A paragraph doing two jobs is two paragraphs. Short sentences carry; long ones bury.
- **Concise, but not terse.** Every sentence earns its place; the reader's time is the budget. Cut the meta ("as mentioned above", "it should be noted that") — the link or the note is the content.
- **GFM, used honestly.** Tables for enumerations, code blocks for anything a reader would copy, admonitions (`> [!NOTE]`, `> [!WARNING]`) for the things a skimming reader must not miss — earned by content, not sprinkled.
- **Emoji are punctuation of last resort.** If an admonition or a bold label does the job, no icon is needed.
- **Examples over descriptions.** One real command beats a paragraph describing the command; one request/response pair beats a paragraph on the shape.
- **No internals.** A human doc names behavior and interfaces, never file paths, package names, or the agent's own context — that material belongs in the `.llms/` tree. When the reader needs the internals, the doc points at the front door, it does not open it.
- **No meta-docs.** License, contributing, changelog live in their own files; a human doc links out rather than duplicating.

## The test

Read the paragraph as the product's user, not its author: could something be deleted without losing the reader anything?
Is any sentence true only because of how the code happens to be written today?
Delete the first; move the second to the ledger where it can be maintained.
