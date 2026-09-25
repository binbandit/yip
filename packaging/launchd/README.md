# launchd

Run `yip service install hub` or `yip service install runner` on macOS. It
writes `~/Library/LaunchAgents/dev.getyip.<role>.plist` (KeepAlive, RunAtLoad,
logs in `~/Library/Logs/yip/`) and prints the `launchctl bootstrap` command to
activate it. Use `--print` to review the definition first.

A LaunchAgent runs in the user's login session. After a reboot on a Mac with
FileVault, the disk must be unlocked and the user logged in before the agent
starts. For unattended recovery, enable automatic login for a dedicated
account, or run the runner in a LaunchDaemon under a dedicated account and
sign providers in under that account. Verify the real behaviour with
`yip doctor` after a reboot; yip reports the actual service state rather than
promising uptime.
