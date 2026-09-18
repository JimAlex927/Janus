import test from "node:test";
import assert from "node:assert/strict";
import { addConfigNode, addMiddlewareToRoute, buildGraph, canConnectNodes, CANVAS_HEIGHT, CANVAS_WIDTH, clampPosition, connectNodes, NODE_HEIGHT, NODE_WIDTH } from "./graph-model.ts";

const node = (id, kind, name) => ({ id, kind, name, subtitle: "", badges: [], x: 0, y: 0 });

test("renders a missing service referenced by a route", () => {
  const graph = buildGraph({
    routes: [{ name: "api", action: { forward: { service: "missing-api" } } }],
  });

  assert.deepEqual(graph.nodes.find(item => item.id === "service:missing-api")?.badges, ["未定义"]);
  assert.ok(graph.edges.some(edge => edge.from === "route:api" && edge.to === "service:missing-api"));
});

test("projects a legacy route service reference to the service edge", () => {
  const graph = buildGraph({
    routes: [{ name: "api", service: "backend" }],
    services: { backend: { upstreams: [] } },
  });

  assert.ok(graph.edges.some(edge => edge.from === "route:api" && edge.to === "service:backend"));
});

test("keeps Middleware out of the top-level graph", () => {
  const graph = buildGraph({
    middlewares: { auth: { in_flight: { max_concurrent: 10 } } },
    routes: [{ name: "api", middlewares: ["auth"] }],
  });

  assert.equal(graph.nodes.some(item => item.kind === "middleware"), false);
  assert.deepEqual(graph.nodes.find(item => item.id === "route:api")?.badges, ["auth"]);
});

test("creates and attaches a Middleware from the Route editor", () => {
  const result = addMiddlewareToRoute({ routes: [{ name: "api", middlewares: [] }] }, "api");

  assert.equal(result.config.middlewares[result.name].buffer.max_response_body_bytes, 1048576);
  assert.deepEqual(result.config.routes[0].middlewares, [result.name]);
});

test("new canvas Routes do not create an implicit Service edge", () => {
  const result = addConfigNode({ services: { api: { upstreams: [] } } }, "route");
  const graph = buildGraph(result.config);

  assert.equal(result.config.routes[0].action.forward.service, "");
  assert.equal(graph.edges.some(edge => edge.from === result.id && edge.to === "service:api"), false);
});

test("keeps a large graph inside the drawable world", () => {
  const routes = Array.from({ length: 18 }, (_, index) => ({ name: `route-${index}`, action: { respond: { status: 200, body: "ok" } } }));
  const graph = buildGraph({ routes });

  assert.ok(graph.nodes.length > routes.length);
  assert.ok(graph.nodes.every(item => item.x >= 0 && item.y >= 0 && item.x + NODE_WIDTH <= CANVAS_WIDTH && item.y + NODE_HEIGHT <= CANVAS_HEIGHT));
});

test("connects a route to a service through the forward action", () => {
  const draft = { routes: [{ name: "api", middlewares: [] }], services: { old: { upstreams: [] }, next: { upstreams: [] } } };
  const result = connectNodes(draft, node("route:api", "route", "api"), node("service:next", "service", "next"));

  assert.equal(result.error, undefined);
  assert.equal(result.config.routes[0].action.forward.service, "next");
});

test("rejects ambiguous shared middleware connections", () => {
  const draft = {
    routes: [
      { name: "one", middlewares: ["auth"] },
      { name: "two", middlewares: ["auth"] },
    ],
    services: { api: { upstreams: [] } },
  };
  const result = connectNodes(draft, node("middleware:auth", "middleware", "auth"), node("service:api", "service", "api"));

  assert.match(result.error, /多个 Route/);
  assert.equal(result.config, draft);
});

test("uses the connection predicate to expose only supported targets", () => {
  const draft = {
    routes: [{ name: "api", middlewares: ["auth"] }],
    services: { backend: { upstreams: [] } },
  };
  const source = node("route:api", "route", "api");

  assert.equal(canConnectNodes(draft, source, node("service:backend", "service", "backend")), true);
  assert.equal(canConnectNodes(draft, source, node("limen:public", "limen", "public")), false);
  assert.equal(canConnectNodes(draft, source, node("middleware:auth", "middleware", "auth")), false);
  assert.equal(canConnectNodes(draft, source, node("middleware:cache", "middleware", "cache")), true);
});

test("connects a service to a middleware without changing route ownership", () => {
  const draft = {
    middlewares: { cache: { buffer: { max_response_body_bytes: 1024 } } },
    services: { api: { upstreams: [], middlewares: [] } },
    routes: [{ name: "api", action: { forward: { service: "api" } } }],
  };
  const result = connectNodes(draft, node("service:api", "service", "api"), node("middleware:cache", "middleware", "cache"));

  assert.equal(result.error, undefined);
  assert.deepEqual(result.config.services.api.middlewares, ["cache"]);
  assert.deepEqual(result.config.routes, draft.routes);
});

test("clamps node positions to the drawable canvas", () => {
  assert.deepEqual(clampPosition({ x: -100, y: -100 }), { x: 12, y: 32 });
  assert.deepEqual(clampPosition({ x: 5000, y: 5000 }), { x: CANVAS_WIDTH - NODE_WIDTH - 12, y: CANVAS_HEIGHT - NODE_HEIGHT - 12 });
});
