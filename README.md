# proxy-ls
This is a language server that acts as a proxy between GNOME Builder and other language servers like vscode-json-languageserver, lemminx or
yaml-language-server.

## Features
- [x] Flatpak manifest support (JSON)
- [ ] Flatpak manifest support (YAML)
- [ ] Support https://www.schemastore.org/json/ for JSON (Would require work in proxy-ls, better place would be in json-language-server)
- [x] Support https://www.schemastore.org/json/ for YAML
- [x] GSchema XML (https://gitlab.gnome.org/GNOME/glib/-/raw/HEAD/gio/gschema.dtd)
- [ ] GResource XML
- [ ] D-Bus (http://www.freedesktop.org/standards/dbus/1.0/introspect.dtd)
- [x] Gitlab CI
- [x] Github Actions
- [ ] Implement splitup GLSL support: https://github.com/svenstaro/glsl-language-server/issues/18#issuecomment-1569054980

