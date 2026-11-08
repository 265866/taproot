# NixOS VM test: services.taproot behind a fake tailscaled.
#
# The server has the tailnet address 100.64.0.1. The client has three more on
# the same link, one per device the fake tailscaled knows: an admin device
# (100.64.0.2), a device with Proxy but not Admin (100.64.0.3), and a device
# with no grant (100.64.0.4).
let
  # taproot.yaml for the test, in one place so a change to the access format
  # changes only this attribute.
  taprootSettings = {
    access.devices = [
      {
        id = "nADMIN";
        name = "laptop";
        proxy = true;
        admin = true;
      }
      {
        id = "nOTHER";
        name = "router";
        proxy = true;
        admin = false;
      }
    ];
  };

  # Answers the LocalAPI calls taproot makes: whois and status. It is a
  # Type=notify service that reports ready once the socket listens, because
  # taproot exits at startup when the socket refuses connections.
  fakeTailscaled =
    pkgs:
    pkgs.writers.writePython3 "fake-tailscaled" { flakeIgnore = [ "E501" ]; } ''
      import json
      import os
      import socket
      import socketserver
      from http.server import BaseHTTPRequestHandler
      from urllib.parse import parse_qs, urlparse

      SOCKET = "/run/tailscale/tailscaled.sock"
      SELF = {"StableID": "nSERVER", "Name": "server.tailnet-0000.ts.net."}
      PEERS = {
          "100.64.0.1": (SELF, "alice@example.com"),
          "100.64.0.2": ({"StableID": "nADMIN", "Name": "laptop.tailnet-0000.ts.net."}, "alice@example.com"),
          "100.64.0.3": ({"StableID": "nOTHER", "Name": "router.tailnet-0000.ts.net."}, "alice@example.com"),
          "100.64.0.4": ({"StableID": "nBOB", "Name": "bob.tailnet-0000.ts.net."}, "bob@example.com"),
      }
      USERS = {"1": "alice@example.com", "2": "bob@example.com"}


      def peer_status(addr):
          node, login = PEERS[addr]
          user = next(k for k, v in USERS.items() if v == login)
          return {"ID": node["StableID"], "HostName": node["Name"].split(".")[0], "DNSName": node["Name"],
                  "OS": "linux", "UserID": int(user), "TailscaleIPs": [addr]}


      def status(peers):
          body = {"BackendState": "Running", "Self": peer_status("100.64.0.1"),
                  "User": {k: {"LoginName": v} for k, v in USERS.items()}}
          if peers:
              body["Peer"] = {addr: peer_status(addr) for addr in PEERS if addr != "100.64.0.1"}
          return body


      class Handler(BaseHTTPRequestHandler):
          def do_GET(self):
              url = urlparse(self.path)
              if url.path == "/localapi/v0/status":
                  body = status(parse_qs(url.query).get("peers", ["true"])[0] != "false")
              elif url.path == "/localapi/v0/whois":
                  addr = parse_qs(url.query).get("addr", [""])[0]
                  if addr not in PEERS:
                      self.send_error(404)
                      return
                  node, login = PEERS[addr]
                  body = {"Node": node, "UserProfile": {"LoginName": login, "DisplayName": login}}
              else:
                  self.send_error(404)
                  return
              data = json.dumps(body).encode()
              self.send_response(200)
              self.send_header("Content-Type", "application/json")
              self.send_header("Content-Length", str(len(data)))
              self.end_headers()
              self.wfile.write(data)

          def log_message(self, fmt, *args):
              print(fmt % args, flush=True)


      os.makedirs(os.path.dirname(SOCKET), exist_ok=True)
      if os.path.exists(SOCKET):
          os.unlink(SOCKET)
      server = socketserver.ThreadingUnixStreamServer(SOCKET, Handler)
      os.chmod(SOCKET, 0o666)
      notify = socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM)
      notify.sendto(b"READY=1", os.environ["NOTIFY_SOCKET"])
      notify.close()
      server.serve_forever()
    '';

  tailnetAddress = address: {
    inherit address;
    prefixLength = 24;
  };
in
{
  name = "taproot";

  nodes.server =
    { pkgs, ... }:
    {
      virtualisation.memorySize = 2048;
      networking.interfaces.eth1.ipv4.addresses = [ (tailnetAddress "100.64.0.1") ];
      # The test has no tailscale0, so the port is opened on the test link.
      networking.firewall.interfaces.eth1.allowedTCPPorts = [ 8317 ];

      systemd.services.tailscaled = {
        wantedBy = [ "multi-user.target" ];
        serviceConfig = {
          Type = "notify";
          ExecStart = fakeTailscaled pkgs;
        };
      };

      services.taproot = {
        enable = true;
        tailscale.address = "100.64.0.1";
        openFirewall = true;
        localModel = true;
        timeZone = "Etc/UTC";
        inherit taprootSettings;
      };
    };

  nodes.client =
    { pkgs, ... }:
    {
      networking.interfaces.eth1.ipv4.addresses = map tailnetAddress [
        "100.64.0.2"
        "100.64.0.3"
        "100.64.0.4"
      ];
      environment.systemPackages = [ pkgs.curl ];
    };

  testScript = ''
    import json

    base = "http://100.64.0.1:8317"


    def get(machine, source, path):
        """Returns (status, body) of GET path from the given source address."""
        out = machine.succeed(
            f"curl -sS --max-time 10 --interface {source} -o /tmp/body -w '%{{http_code}}' {base}{path}; echo; cat /tmp/body"
        )
        status, _, body = out.partition("\n")
        return int(status), body


    start_all()
    server.wait_for_unit("taproot.service")
    server.wait_for_open_port(8317, "100.64.0.1")
    client.wait_for_unit("multi-user.target")

    with subtest("the admin device gets the dashboard and the API"):
        status, body = get(client, "100.64.0.2", "/")
        assert status == 200, (status, body)
        # The real build, not the placeholder a build without the dashboard serves.
        assert "/assets/" in body, body
        status, body = get(client, "100.64.0.2", "/taproot/api/v1/me")
        assert status == 200, (status, body)
        me = json.loads(body)
        assert me["admin"] is True and me["proxy"] is True and me["nodeId"] == "nADMIN", me
        status, body = get(client, "100.64.0.2", "/taproot/api/v1/access/devices")
        assert status == 200, (status, body)
        devices = {d["id"]: d for d in json.loads(body)["devices"]}
        assert devices["nSERVER"]["server"] is True, devices
        assert devices["nOTHER"]["proxy"] is True and devices["nOTHER"]["admin"] is False, devices
        assert devices["nBOB"]["onTailnet"] is True and devices["nBOB"]["proxy"] is False, devices

    with subtest("a device with Proxy but not Admin reaches the proxy only"):
        status, body = get(client, "100.64.0.3", "/v1/models")
        assert status == 200, (status, body)
        status, body = get(client, "100.64.0.3", "/taproot/api/v1/me")
        assert status == 403, (status, body)
        error = json.loads(body)["error"]
        assert error["code"] == "not_admin" and error["nodeId"] == "nOTHER", body

    with subtest("a device with no grant is refused the proxy and the API"):
        status, body = get(client, "100.64.0.4", "/v1/models")
        assert status == 403, (status, body)
        assert json.loads(body)["error"]["code"] == "no_proxy", body
        status, body = get(client, "100.64.0.4", "/taproot/api/v1/me")
        assert status == 403, (status, body)
        assert json.loads(body)["error"]["code"] == "not_admin", body

    with subtest("processes on the server are not admins"):
        status, body = get(server, "100.64.0.1", "/taproot/api/v1/me")
        assert status == 403, (status, body)
        server.fail("curl -sS --max-time 5 http://127.0.0.1:8317/healthz")
        server.succeed("systemctl show -p IPAddressDeny taproot.service | grep -F 127.0.0.0/8")

    with subtest("config.yaml is rewritten on start and taproot.yaml is kept"):
        server.succeed("echo '# edited' >> /var/lib/taproot/config.yaml")
        server.succeed("echo '# edited' >> /var/lib/taproot/taproot.yaml")
        server.succeed("test ! -L /var/lib/taproot/config.yaml && test ! -L /var/lib/taproot/taproot.yaml")
        server.succeed("systemctl restart taproot.service")
        server.wait_for_open_port(8317, "100.64.0.1")
        server.fail("grep -F '# edited' /var/lib/taproot/config.yaml")
        server.succeed("grep -F '# edited' /var/lib/taproot/taproot.yaml")
        server.succeed("stat -c %a /var/lib/taproot/config.yaml | grep -x 600")
        status, _ = get(client, "100.64.0.2", "/taproot/api/v1/me")
        assert status == 200, status
  '';
}
