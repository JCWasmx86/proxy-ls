# proxy-ls
This is a language server that acts as a proxy between GNOME Builder and other language servers like vscode-json-languageserver, lemminx or
yaml-language-server. This is necessary as GNOME Builder currently does not support configuring language servers.

## Features
- [x] Flatpak manifest support (JSON)
- [x] Flatpak manifest support (YAML)
- [x] Github Actions
- [x] Gitlab CI
- [x] GResource XML
- [x] GSchema XML (https://gitlab.gnome.org/GNOME/glib/-/raw/HEAD/gio/gschema.dtd)
- [x] Rome
- [x] Ruff
- [x] Support https://www.schemastore.org/json/ for YAML
- [ ] Appstream support
- [ ] D-Bus (http://www.freedesktop.org/standards/dbus/1.0/introspect.dtd)
- [ ] Implement splitup GLSL support: https://github.com/svenstaro/glsl-language-server/issues/18#issuecomment-1569054980
- [ ] Support https://www.schemastore.org/json/ for JSON (Would require work in proxy-ls, better place would be in json-language-server)

## Installation
### Editor-Side
> [!IMPORTANT]
> Requires GNOME Builder Nightly!

- Install the proxyls plugin from here: https://github.com/JCWasmx86/GNOME-Builder-Plugins
### Dependencies
> [!IMPORTANT]
> *All* of these dependencies are needed. If one language server is missing, proxy-ls may fail in weird way!
#### YAML Language Server
```
sudo npm install -g yaml-language-server
```
#### Lemminx (XML Language Server)
Follow these steps: https://github.com/eclipse/lemminx#generating-a-native-binary

The binary you copy to `/usr/local/bin` should be called `lemminx`. It is recommended to use a native binary
as opposed to e.g. using a JAR file and wrapping it using a shellscript as it improves the startup time at the
cost of a little bit of performance.
#### JSON Language Server
```
git clone https://github.com/microsoft/vscode --depth=1
cd vscode/extensions/json-language-features/server
npm i
tsc -p ./
npm pack
sudo npm install -g vscode-json-languageserver-*.tgz
```
This is required as a plain `npm install -g` would symlink from /usr/local/... to your
vscode directory.
#### ruff-lsp
```
sudo pip install ruff-lsp # Or you can install my fork from here: https://github.com/JCWasmx86/ruff-lsp for a better experience
sudo pip install ruff
```
#### rome
```
cargo install --git https://github.com/rome/tools rome_cli
```
### Language Server
(Requires go to be installed)
```
git clone https://github.com/JCWasmx86/proxy-ls
cd proxy-ls
make
sudo make install
```
## Objectives
### Goals
- Enable better XML/JSON/YAML integration in GNOME Builder
- Minimal amount of code. Work should be done on the language server side
### Non-Goals
- macOS/Windows support
- Support for any other editor
- Support for weird Linux distributions like nixOS or Alpine.
- A lot of configuration knobs

## License
GNU GPL v3.0

