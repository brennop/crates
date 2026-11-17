// status: is Music running? Never launches it.
// argv: none. Result: {"running": bool}
const NEEDS_RUNNING = false;
function main(argv, music) {
  return { running: music.running() };
}
