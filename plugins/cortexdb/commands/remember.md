---
description: Save a fact, preference, or decision to CortexDB memory
---

Store the following into the CortexDB global memory by calling the `memory_save` MCP tool. Choose a stable, descriptive `memory_id` and concise `content`; use `scope=user` with `namespace=assistant` unless the user implies otherwise. If it is durable reference knowledge (not a personal preference), use `knowledge_save` instead. For a fact or a lesson, say how it is known: `grade: "verified"` with `grade_why` naming the check (the command and its output, the test, the reproduction) only when it was actually checked; `grade: "asserted"` for anything not checked — a suspected cause, an untested fix — and write the content so it says it is unverified. Leave `grade` out for a preference or a decision. Confirm what you stored in one line, with its grade.

Remember this:

$ARGUMENTS
