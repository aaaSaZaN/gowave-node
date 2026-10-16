The message schemas are copied unchanged from XTLS/Xray-core **v26.7.28**:

- `app/stats/command/command.proto`
- `proxy/hysteria/account/config.proto`

`go_package` mappings are overridden only during generation; protobuf wire
package names and field numbers remain upstream-compatible. Run
`scripts/generate-proto.sh` with protoc, the repository Go toolchain, curl, and
Python 3 installed to regenerate messages and the small gRPC client.

The existing Xray dependency supplies stable HandlerService/account messages.
The new stats bindings replace the old stats package rather than registering
conflicting copies of the same protobuf names. Hysteria now uses a generated
account message instead of handcrafted protobuf bytes.
