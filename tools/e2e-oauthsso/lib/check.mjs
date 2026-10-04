// The ladder's scoreboard: one list per verdict, one line per probe,
// and the exit the runner reads.
const pass = []
const fail = []

export function check(name, cond, detail = '') {
  ;(cond ? pass : fail).push(name)
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}${detail && !cond ? `  — ${detail}` : ''}`)
}

export function finish() {
  console.log()
  console.log(`ladder: ${pass.length} passed, ${fail.length} failed`)
  if (fail.length > 0) {
    console.log(`failed: ${fail.join(', ')}`)
    process.exit(1)
  }
}
