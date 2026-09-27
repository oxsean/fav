---
name: fix
description: implement, test, accept
stages:
  - {name: implement, role: implement, check: true}
  - {name: test, role: test, output: verdict, on_rework: implement}
  - {name: accept, gate: human}
max_loops: 2
---
