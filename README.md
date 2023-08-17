# proxy-ls
This is a language server that acts as a proxy between GNOME Builder and other language servers like vscode-json-languageserver, lemminx or
yaml-language-server.

## Features
- [x] Flatpak manifest support (JSON)
- [x] Flatpak manifest support (YAML)
- [ ] Support https://www.schemastore.org/json/ for JSON (Would require work in proxy-ls, better place would be in json-language-server)
- [x] Support https://www.schemastore.org/json/ for YAML
- [x] GSchema XML (https://gitlab.gnome.org/GNOME/glib/-/raw/HEAD/gio/gschema.dtd)
- [ ] GResource XML
- [ ] D-Bus (http://www.freedesktop.org/standards/dbus/1.0/introspect.dtd)
- [x] Gitlab CI
- [x] Github Actions
- [ ] Implement splitup GLSL support: https://github.com/svenstaro/glsl-language-server/issues/18#issuecomment-1569054980
- [ ] Appstream support

## Installation
### Editor-Side
> [!IMPORTANT]
> Requires GNOME Builder Nightly!

- Install the proxyls plugin from here: https://github.com/JCWasmx86/GNOME-Builder-Plugins
### Language Server
#### Dependencies
> [!IMPORTANT]
> *All* of these dependencies are needed. If one language server is missing, proxy-ls may fail in weird way!
##### YAML Language Server
```
sudo npm install -g yaml-language-server
```
##### Lemminx (XML Language Server)
Follow these steps: https://github.com/eclipse/lemminx#generating-a-native-binary

The binary you copy to `/usr/local/bin` should be called `lemminx`. It is recommended to use a native binary
as opposed to e.g. using a JAR file and wrapping it using a shellscript.
##### JSON Language Server
```
git clone https://github.com/microsoft/vscode --depth=1
cd vscode/extensions/json-language-features/server
npm i
tsc -p ./
npm pack
sudo npm i -g vscode-json-languageserver-*.tgz
```

