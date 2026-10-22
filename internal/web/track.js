// The tracking page of one Job. Every text from a Job goes in as a text
// node, never as markup, because issue text can steer what the model writes.
"use strict";

const token = new URLSearchParams(location.search).get("token") || "";
const jobID = document.body.dataset.job;
const url = (path) => "/jobs/" + jobID + path + "?token=" + encodeURIComponent(token);

// stoppedAt is when the page lost loopd; problem is an error loopd answered.
const view = { track: null, events: [], following: false, stoppedAt: null, problem: "" };

// el builds an element; strings among the children become text nodes.
function el(tag, cls, ...children) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  for (const c of children) {
    if (c === null || c === undefined || c === false) continue;
    e.append(typeof c === "string" ? document.createTextNode(c) : c);
  }
  return e;
}

const svgNS = "http://www.w3.org/2000/svg";
const icons = {
  check: [["path", { d: "M20 6 9 17l-5-5" }]],
  x: [["path", { d: "M18 6 6 18" }], ["path", { d: "m6 6 12 12" }]],
  pause: [["rect", { x: "14", y: "4", width: "4", height: "16", rx: "1", fill: "currentColor" }], ["rect", { x: "6", y: "4", width: "4", height: "16", rx: "1", fill: "currentColor" }]],
  chevron: [["path", { d: "m9 18 6-6-6-6" }]],
  external: [["path", { d: "M15 3h6v6" }], ["path", { d: "M10 14 21 3" }], ["path", { d: "M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" }]],
  ring: [["circle", { cx: "12", cy: "12", r: "9" }], ["circle", { cx: "12", cy: "12", r: "3", fill: "currentColor", stroke: "none" }]],
  hollow: [["circle", { cx: "12", cy: "12", r: "9" }]],
};

function icon(name, cls) {
  const svg = document.createElementNS(svgNS, "svg");
  for (const [k, v] of Object.entries({ viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", "stroke-width": "2", "stroke-linecap": "round", "stroke-linejoin": "round", "aria-hidden": "true" })) {
    svg.setAttribute(k, v);
  }
  if (cls) svg.setAttribute("class", cls);
  for (const [tag, attrs] of icons[name]) {
    const shape = document.createElementNS(svgNS, tag);
    for (const [k, v] of Object.entries(attrs)) shape.setAttribute(k, v);
    svg.append(shape);
  }
  return svg;
}

// stage names: "drill", "implement 2", "verify 2", "rubric 2", "land", "publish"
const kindOf = (stage) => stage.split(" ")[0];
const numOf = (stage) => Number(stage.split(" ")[1] || 0);
const stageTitle = (stage) => {
  if (stage === "publish") return "PR";
  return stage.charAt(0).toUpperCase() + stage.slice(1);
};

const time = (t) => (t ? new Date(t) : null);
const clock = (d) => d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
const money = (n) => "$" + n.toFixed(2);
const prNumber = (job) => (job.PRURL.match(/\/pull\/(\d+)/) || [])[1];

function duration(ms) {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return s + "s";
  const m = Math.floor(s / 60);
  if (m < 60) return m + "m " + String(s % 60).padStart(2, "0") + "s";
  return Math.floor(m / 60) + "h " + String(m % 60).padStart(2, "0") + "m";
}

const runningStates = new Set(["queued", "running"]);
const isLive = () => runningStates.has(view.track.job.State) && !view.stoppedAt;

// next is the stage that starts after row ends, while the Job runs on.
function next(row) {
  const n = numOf(row.stage);
  switch (kindOf(row.stage)) {
    case "drill": return "implement 1";
    case "implement": return "verify " + n;
    case "verify": return row.status === "pass" ? "rubric " + n : "implement " + (n + 1);
    case "rubric": return row.status === "revise" ? "implement " + (n + 1) : "land";
    case "land": return "publish";
  }
  return null;
}

// current is the running stage and when it started, or null. A handed-back
// Job runs stage names again, so only a row that ended after the stage's
// first event in this run ends it.
function current() {
  if (view.track.job.State !== "running") return null;
  const rows = view.track.stages;
  const last = rows[rows.length - 1];
  const firstOf = (stage) => view.events.find((e) => e.stage === stage);
  const latest = view.events[view.events.length - 1];
  let stage = latest ? latest.stage : null;
  if (stage !== null) {
    const since = time(firstOf(stage).time);
    if (rows.some((r) => r.stage === stage && time(r.end) >= since)) stage = next(rows.findLast((r) => r.stage === stage));
  } else if (last) {
    stage = next(last);
  }
  if (stage === null) return null;
  const first = firstOf(stage);
  const start = first ? time(first.time) : last ? time(last.end) : time(view.track.job.Started);
  return { stage, start };
}

// run is the implement run the Job is on, or ended on: the number of its
// current or latest implement, verify or rubric stage.
function run(cur) {
  const stages = view.track.stages.map((r) => r.stage).concat(cur ? [cur.stage] : []);
  const numbered = stages.filter((s) => numOf(s) > 0);
  return numbered.length ? numOf(numbered[numbered.length - 1]) : 0;
}

function runOf(n) {
  const max = view.track.max_iterations;
  return max ? n + " of " + max : String(n);
}

function headline(state, cur) {
  switch (state) {
    case "queued": return "Queued";
    case "done": return "Draft PR opened";
    case "blocked": return "Waiting for your answer";
    case "failed": return "Job failed";
    case "exhausted": return view.track.stages.at(-1)?.bound === "timeout" ? "Out of time" : "Out of attempts";
    case "manual": return "Taken over";
  }
  if (!cur) return "Starting";
  const n = numOf(cur.stage);
  switch (kindOf(cur.stage)) {
    case "drill": return "Drilling";
    case "implement": return "Implementing, run " + runOf(n);
    case "verify": return "Running verify " + n;
    case "rubric": return "Reviewing, rubric " + n;
    case "land": return "Landing";
    case "publish": return "Opening the PR";
  }
  return "Running " + cur.stage;
}

const stateIcon = { running: "ring", queued: "hollow", done: "check", blocked: "pause", manual: "pause", failed: "x", exhausted: "x" };

function detail(job) {
  const box = el("div", "detail");
  const command = (text) => el("code", "cmd", text);
  switch (job.State) {
    case "done": {
      const pr = prNumber(job);
      const link = el("a", "pr-link", "Open PR" + (pr ? " #" + pr : ""), icon("external"));
      link.href = job.PRURL;
      link.target = "_blank";
      link.rel = "noopener noreferrer";
      box.append(link, command("loopctl try " + job.ID));
      break;
    }
    case "blocked": box.append(el("p", "text", job.Question), command("loopctl takeover " + job.ID)); break;
    case "failed": box.append(el("p", "text", job.Error), command((job.Worktree ? "loopctl takeover " : "loopctl rm ") + job.ID)); break;
    case "exhausted": box.append(el("p", "text", job.Summary), command("loopctl takeover " + job.ID)); break;
    case "manual": box.append(command("loopctl handback " + job.ID)); break;
    default: return null;
  }
  return box;
}

function elapsed(job) {
  const started = time(job.Started);
  if (!started) return "";
  const end = view.stoppedAt || (isLive() ? new Date() : time(job.Ended));
  return end ? duration(end - started) : "";
}

function cost(job) {
  if (!runningStates.has(job.State)) return money(job.CostUSD);
  return money(view.track.stages.reduce((sum, r) => sum + r.cost_usd, 0));
}

function figure(label, value, cls) {
  return el("div", "figure", el("span", "label", label), el("span", "value " + (cls || ""), value));
}

const endedInFailure = new Set(["failed", "exhausted"]);
const steps = [["drill", "Drill"], ["implement", "Implement"], ["verify", "Verify"], ["rubric", "Rubric"], ["land", "Land"], ["publish", "PR"]];

function stepper(job, cur) {
  const rows = view.track.stages;
  const lastRow = rows[rows.length - 1];
  const list = el("ol", "stepper");
  for (const [kind, name] of steps) {
    const mine = rows.filter((r) => kindOf(r.stage) === kind);
    const last = mine[mine.length - 1];
    let state = "pending";
    let note = "";
    if (cur && kindOf(cur.stage) === kind) {
      state = "current";
    } else if (kind === "drill" && !job.Work.startsWith("issue ")) {
      state = "skipped";
    } else if (last) {
      state = { done: "done", pass: "done", blocked: "blocked" }[last.status] || "failed";
      if (job.State === "blocked" && last === lastRow) state = "blocked";
    }
    // a failed verify or rubric stays pending while a later run can still pass it
    const gateFailed = (kind === "verify" || kind === "rubric") && state === "failed";
    const retried = gateFailed && !(endedInFailure.has(job.State) && last === lastRow);
    if (retried) state = "pending";
    const runs = mine.length + (state === "current" ? 1 : 0);
    if (state === "skipped") {
      note = "skipped";
    } else if (gateFailed) {
      note = last.stage + " " + (kind === "verify" ? "failed" : last.status);
    } else if (kind === "publish") {
      note = prNumber(job) ? "#" + prNumber(job) : "";
    } else if (runs) {
      note = runs === 1 ? "1 run" : runs + " runs";
    }
    const node = el("span", "node");
    if (state === "done") node.append(icon("check"));
    if (state === "failed") node.append(icon("x"));
    if (state === "blocked") node.append(icon("pause"));
    list.append(el("li", "step st-" + state, node, el("span", "step-name", name), el("span", "step-note" + (retried ? " note-failed" : ""), note || " ")));
  }
  return list;
}

function card(job) {
  const cur = current();
  const state = job.State;
  let label = state === "manual" ? "taken over" : state;
  if (view.stoppedAt) label = "last seen " + label;
  const text = el("div", "state-text",
    el("span", "state-label", label),
    el("h1", "headline", headline(state, cur)),
    detail(job));
  const n = run(cur);
  const figures = el("div", "figures",
    figure("Elapsed", elapsed(job), "elapsed"),
    figure("Cost", cost(job)),
    figure("Run", n ? runOf(n) : ""));
  return el("section", "card s-" + state + (view.stoppedAt ? " stale" : ""),
    el("div", "card-top", el("div", "state-icon", icon(stateIcon[state] || "hollow")), text, figures),
    stepper(job, cur));
}

function meta(job) {
  const [kind, ...rest] = job.Work.split(" ");
  const label = { spec: "Spec", task: "Task", issue: "Issue" }[kind] || "Work";
  let value = rest.join(" ");
  if (kind === "issue") value = "#" + value;
  const pair = (l, v) => el("div", null, el("span", "label", l), el("span", "value", v));
  return el("div", "meta", pair(label, value), pair("Branch", job.Branch));
}

function logEntry(e, newest) {
  const sub = e.sub ? " sub" : "";
  let cls = "entry";
  let body;
  if (e.tool) {
    cls += " call";
    body = el("span", "body", el("span", "tool", e.tool), e.text ? "(" + e.text + ")" : "");
  } else if (!e.bound && e.text.startsWith("│ ")) {
    cls += " out";
    body = el("span", "body", e.text.slice(2));
  } else if (e.bound) {
    cls += " call";
    body = el("span", "body", e.text);
  } else {
    cls += " say";
    body = el("span", "body", e.text);
  }
  return el("div", cls + sub + (newest ? " newest" : ""), el("span", "dot"), body, el("span", "time", clock(new Date(e.time))));
}

function live() {
  const cur = current();
  const stage = cur ? cur.stage : null;
  const first = view.events.findIndex((e) => e.stage === stage);
  const entries = first < 0 ? [] : view.events.slice(first).filter((e) => e.stage === stage && !(e.bound && e.text === "start"));
  const log = el("div", "log");
  entries.forEach((e, i) => log.append(logEntry(e, i === entries.length - 1)));
  if (!entries.length) log.append(el("p", "empty", stage ? "Waiting for the first tool call of " + stageTitle(stage) : "Waiting for the first stage"));
  return el("section", "section live",
    el("div", "section-head", el("h2", null, "Live"), el("span", "muted", stage ? stageTitle(stage) : "")),
    log);
}

function cells(row, end) {
  const start = time(row.start);
  const noCost = kindOf(row.stage) === "verify" || row.stage === "publish" || row.status === "running";
  return [
    el("span", null, el("span", "chip s-" + row.status, row.status)),
    el("span", null, stageTitle(row.stage)),
    el("span", "num started", clock(start)),
    el("span", "num", duration((end || new Date()) - start)),
    el("span", "num cost", noCost ? "" : money(row.cost_usd)),
  ];
}

function gateDetail(row) {
  const box = el("div", "gate");
  for (const c of row.commands || []) {
    box.append(el("div", "gate-cmd",
      el("span", "muted", c.name),
      el("span", "line", c.line),
      el("span", "chip s-" + (c.exit_code === 0 ? "pass" : "fail"), c.exit_code === 0 ? "pass" : "exit " + c.exit_code)));
  }
  if (row.output && row.output.length) box.append(el("pre", "gate-out", row.output.join("\n")));
  return box;
}

function stagesTable() {
  const rows = view.track.stages.slice();
  const cur = current();
  if (cur && !view.stoppedAt) rows.push({ stage: cur.stage, status: "running", start: cur.start, cost_usd: 0 });
  const table = el("div", "panel stages",
    el("div", "stages-row stages-head", el("span", null, "Status"), el("span", null, "Stage"), el("span", "num started", "Started"), el("span", "num", "Took"), el("span", "num cost", "Cost"), el("span")));
  rows.forEach((row, i) => {
    const end = row.status === "running" ? null : time(row.end);
    if (row.status === "fail" && row.commands) {
      const summary = el("summary", null, ...cells(row, end), icon("chevron", "chev"));
      const details = el("details", null, summary, gateDetail(row));
      details.dataset.row = String(i);
      table.append(details);
    } else {
      table.append(el("div", "stages-row", ...cells(row, end), el("span")));
    }
  });
  if (!rows.length) table.append(el("div", "stages-row muted", el("span", null, "No stage has ended yet")));
  return el("section", "section", el("div", "section-head", el("h2", null, "Stages")), table);
}

function notice() {
  if (view.problem) return el("div", "notice", el("strong", null, "loopd returned an error"), el("span", "muted", " · " + view.problem));
  if (view.stoppedAt) return el("div", "notice", el("strong", null, "loopd stopped"), el("span", "muted", " · reload this page; if it does not load, run loopctl install"));
  return null;
}

function render() {
  const page = document.getElementById("page");
  if (!view.track) {
    page.replaceChildren(notice());
    return;
  }
  const job = view.track.job;
  document.getElementById("loop").textContent = job.Loop;
  const open = new Set([...document.querySelectorAll(".stages details[open]")].map((d) => d.dataset.row));
  const log = document.querySelector(".log");
  const scrolled = log ? log.scrollTop : 0;
  const atBottom = !log || scrolled + log.clientHeight >= log.scrollHeight - 4;
  const parts = [notice(), card(job), meta(job)];
  if (view.following && isLive()) parts.push(live());
  parts.push(stagesTable());
  page.replaceChildren(...parts.filter(Boolean));
  for (const d of document.querySelectorAll(".stages details")) d.open = open.has(d.dataset.row);
  const newLog = document.querySelector(".log");
  if (newLog) newLog.scrollTop = atBottom ? newLog.scrollHeight : scrolled;
}

// renderSoon draws the page once per frame however many events arrive in it.
let frame = 0;
function renderSoon() {
  if (!frame) frame = requestAnimationFrame(() => { frame = 0; render(); });
}

// Answered is an error that loopd answered, as opposed to loopd gone.
class Answered extends Error {}

// fail shows why the page lost its data: loopd's own error, or loopd gone.
function fail(err) {
  if (err instanceof Answered) view.problem = err.message;
  else view.stoppedAt = view.stoppedAt || new Date();
  render();
}

async function load() {
  const res = await fetch(url("/data"));
  if (!res.ok) throw new Answered((await res.text()).trim());
  const track = await res.json();
  track.stages = track.stages || [];
  view.track = track;
  view.problem = "";
}

let reload = null;
function reloadSoon() {
  clearTimeout(reload);
  reload = setTimeout(() => load().then(render, fail), 250);
}

// follow reads the Job's events until it ends, then shows how it ended. A
// page that fell behind the Job starts over from the Job's earlier events.
async function follow() {
  view.following = true;
  view.events = [];
  let ended = false;
  let error = "";
  try {
    const res = await fetch(url("/events"));
    if (!res.ok) throw new Answered((await res.text()).trim());
    const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
    let buf = "";
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += value;
      let i;
      while ((i = buf.indexOf("\n")) >= 0) {
        const line = JSON.parse(buf.slice(0, i));
        buf = buf.slice(i + 1);
        if (line.event) {
          view.events.push(line.event);
          if (line.event.bound) reloadSoon();
          renderSoon();
        } else if (line.job) {
          ended = true;
        } else {
          error = line.error;
        }
      }
    }
  } catch (err) {
    if (err instanceof Answered) error = err.message;
  }
  if (error === "viewer fell behind") return follow();
  view.following = false;
  clearTimeout(reload);
  if (ended) return load().then(render, fail);
  if (error && error !== "loopd stopped") return fail(new Answered(error));
  fail(new Error("loopd stopped"));
}

async function main() {
  try {
    await load();
  } catch (err) {
    return fail(err);
  }
  render();
  setInterval(() => {
    if (!view.track || !isLive()) return;
    const value = document.querySelector(".figure .elapsed");
    if (value) value.textContent = elapsed(view.track.job);
  }, 1000);
  if (isLive()) await follow();
}

main();
