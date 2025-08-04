---
publish: true
---

# Test Markdown Link Protection

This should work correctly:
- Normal wikilink: [[Some Note]]
- Markdown link with wikilink inside: [PRD]([[Some Note]])
- Normal markdown link: [Google](https://google.com)

The second one should NOT create malformed HTML. 