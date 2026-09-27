---
name: feature
description: implement, review, accept
stages:
  - {name: implement, role: implement, check: true}
  - {name: review, role: review, output: verdict, on_rework: implement}
  - {name: accept, gate: human}
max_loops: 2
---
