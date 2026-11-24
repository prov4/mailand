#pragma once

// Headless end-to-end check of Rpc / FolderModel / MessageModel / MessageView against a running
// daemon (no WebEngine, no display). Prints a compact report; returns the process exit code.
int runSelfTest(int argc, char **argv);
