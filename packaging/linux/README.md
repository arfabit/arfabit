# Linux packaging

Not built yet beyond these files (docs/RELEASE.md §3, §5, §12 step 5).

- `arfabit.desktop` is the app menu entry. It opens ARFABIT's page, since
  ARFABIT itself is already running as a service. The package installs it in
  `/usr/share/applications/`.
- Its icon is `arfabit.png`, made by `go run ./packaging/icon`, which the
  package installs as `/usr/share/icons/hicolor/256x256/apps/arfabit.png`.
