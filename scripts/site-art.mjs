#!/usr/bin/env node
// Timelines for the animated illustrations on the website. Run `node scripts/site-art.mjs` after
// editing them: it rewrites the generated block in site/site.css. site.test.mjs fails when that
// block is out of date.
//
// One rule for colour: a box is blue while it holds the request and green while it holds the
// answer. A dot carries each message along the lines between boxes.
import { readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const FADE = 0.09; // seconds for a box to change colour
const BLINK = 0.01; // seconds for a dot to appear or disappear
const EASE = 'cubic-bezier(.45, 0, .55, 1)';

const STATE = {
  blue: { border: 'var(--accent)', background: 'var(--accent-soft)', text: 'var(--accent)' },
  green: { border: 'var(--success)', background: 'var(--success-soft)', text: 'var(--success)' },
};

// Paths a dot can follow, in percent of its parent box. A wire is a vertical line; a branch box
// spans from the centre of the fork to one program, so the dot can follow the elbow.
const DOWN = [[50, 0], [50, 100]];
const RIGHT_ELBOW = [[0, 0], [0, 50], [100, 50], [100, 100]];
const LEFT_ELBOW = [[100, 0], [100, 50], [0, 50], [0, 100]];
const ELBOW_WEIGHTS = [1, 8, 1];
const reverse = (points) => [...points].reverse();

// The trip inside the user's device, the same in every illustration: WebDeviceCable takes the
// request, hands it to one program down its branch, and returns that program's answer.
function deviceTrip(t, program) {
  const elbow = program === 'claude' ? RIGHT_ELBOW : LEFT_ELBOW;
  const side = program === 'claude' ? 'right' : 'left';
  return {
    boxes: [
      ['wdc', t, t + 0.2, 'blue'],
      [program, t + 0.8, t + 1.7, 'blue'],
      [program, t + 1.8, t + 2.05, 'green'],
      ['wdc', t + 2.6, t + 2.8, 'green'],
    ],
    dots: [
      [`${side}-send`, { from: t + 0.25, to: t + 0.75, points: elbow, weights: ELBOW_WEIGHTS }],
      [`${side}-reply`, { from: t + 2.05, to: t + 2.55, points: reverse(elbow), weights: ELBOW_WEIGHTS }],
    ],
  };
}

// A website talks straight to WebDeviceCable: the home page hero and the developers page's
// "From your web page" option. 7.5s per trip; the first runs in Claude Code, the second in Codex.
// The answer stays on the page until the next trip starts: the button stays green and, on the
// home page, "Summarized" shows beside it.
function directTrip(t, program) {
  const device = deviceTrip(t + 1.65, program);
  return {
    boxes: [
      ['ask', t + 0.3, t + 0.75, 'blue'],
      ...device.boxes,
      ['ask', t + 5.35, t + 7.35, 'green'],
      ['done', t + 5.45, t + 7.35, 'green'],
    ],
    dots: [
      ['send', { from: t + 0.8, to: t + 1.6, points: DOWN, ease: true }],
      ...device.dots,
      ['reply', { from: t + 4.5, to: t + 5.3, points: reverse(DOWN), ease: true }],
    ],
  };
}

// A backend talks to WebDeviceCable through a relay. The device asks the relay for work first
// (the ring waits at the relay), your server sends a request, and the relay hands it over as the
// reply to that ask. The answer goes back up the same way. 8s per trip.
function relayTrip(t, program) {
  const device = deviceTrip(t + 3, program);
  return {
    boxes: [
      ['server', t + 1, t + 1.4, 'blue'],
      ['relay', t + 2.1, t + 2.3, 'blue'],
      ...device.boxes,
      ['relay', t + 6.5, t + 6.7, 'green'],
      ['server', t + 7.4, t + 7.9, 'green'],
    ],
    dots: [
      ['poll', { from: t + 0.2, to: t + 0.9, hold: t + 2.3, points: reverse(DOWN), ease: true }],
      ['send-1', { from: t + 1.45, to: t + 2.05, points: DOWN, ease: true }],
      ['send-2', { from: t + 2.35, to: t + 2.95, points: DOWN, ease: true }],
      ...device.dots,
      ['reply-2', { from: t + 5.85, to: t + 6.45, points: reverse(DOWN), ease: true }],
      ['reply-1', { from: t + 6.75, to: t + 7.35, points: reverse(DOWN), ease: true }],
    ],
  };
}

// Boxes rest on their own background. `text` boxes also change their text colour; `shown`
// parts are hidden at rest and appear while their interval lasts.
const BOXES = {
  ask: { rest: 'var(--surface-raised)', text: true },
  done: { shown: true },
  wdc: { rest: 'var(--surface)' },
  claude: { rest: 'var(--surface)' },
  codex: { rest: 'var(--surface)' },
  server: { rest: 'var(--surface)' },
  relay: { rest: 'var(--surface)' },
};

export const FLOWS = [
  { name: 'direct', length: 15, trips: [directTrip(0, 'claude'), directTrip(7.5, 'codex')] },
  { name: 'relay', length: 16, trips: [relayTrip(0, 'claude'), relayTrip(8, 'codex')] },
];

const pct = (t, length) => `${+(t / length * 100).toFixed(3)}%`;

function boxStyle(role, state) {
  const box = BOXES[role];
  if (box.shown) return `opacity: ${state ? 1 : 0};`;
  const look = state ? STATE[state] : { border: 'var(--border)', background: box.rest, text: 'var(--text)' };
  return `border-color: ${look.border}; background-color: ${look.background};${box.text ? ` color: ${look.text};` : ''}`;
}

// Keyframes that hold the resting look except during each interval, fading in and out. Two
// intervals that touch (blue, then green) change straight from one colour to the next.
function boxKeyframes(role, intervals, length) {
  const points = [[0, null]];
  intervals.sort((a, b) => a[0] - b[0]);
  intervals.forEach(([from, to, state], i) => {
    const last = points[points.length - 1];
    if (last[1] === null) points.push([Math.max(from - FADE, last[0]), null]);
    points.push([from, state], [to, state]);
    const next = intervals[i + 1];
    if (!next || next[0] - to > 2 * FADE) points.push([to + FADE, null]);
  });
  points.push([length, null]);
  return groupFrames(points.map(([t, state]) => [pct(t, length), boxStyle(role, state)]));
}

function dotKeyframes(travels, length) {
  travels.sort((a, b) => a.from - b.from);
  const at = ([left, top], opacity) => `left: ${left}%; top: ${top}%; opacity: ${opacity};`;
  const frames = [];
  let rest = travels[0].points[0];
  for (const travel of travels) {
    const { from, to, points } = travel;
    const weights = travel.weights ?? points.slice(1).map(() => 1);
    const total = weights.reduce((sum, w) => sum + w, 0);
    frames.push([from - BLINK, at(points[0], 0)]);
    frames.push([from, at(points[0], 1) + (travel.ease ? ` animation-timing-function: ${EASE};` : '')]);
    let time = from;
    points.slice(1).forEach((point, i) => {
      time += (to - from) * weights[i] / total;
      frames.push([time, at(point, 1)]);
    });
    const end = travel.hold ?? to;
    if (end > to) frames.push([end, at(points.at(-1), 1)]);
    frames.push([end + BLINK, at(points.at(-1), 0)]);
    rest = points.at(-1);
  }
  frames.unshift([0, at(travels[0].points[0], 0)]);
  frames.push([length, at(rest, 0)]);
  return groupFrames(frames.map(([t, style]) => [pct(t, length), style]));
}

// Joins neighbouring keyframes with the same style into one rule, as in `5%, 12% { … }`.
function groupFrames(frames) {
  const rules = [];
  for (const [offset, style] of frames) {
    const last = rules[rules.length - 1];
    if (last && last.style === style && !style.includes('animation-timing-function')) {
      if (last.offsets.at(-1) !== offset) last.offsets.push(offset);
    } else {
      rules.push({ offsets: [offset], style });
    }
  }
  return rules.map((rule) => `  ${keepEnds(rule.offsets).join(', ')} { ${rule.style} }`).join('\n');
}
const keepEnds = (offsets) => (offsets.length > 2 ? [offsets[0], offsets.at(-1)] : offsets);

export function artCss() {
  const keyframes = [];
  const bindings = [];
  for (const flow of FLOWS) {
    const boxes = new Map();
    const dots = new Map();
    for (const trip of flow.trips) {
      for (const [role, from, to, state] of trip.boxes) {
        if (!boxes.has(role)) boxes.set(role, []);
        boxes.get(role).push([from, to, state]);
      }
      for (const [role, travel] of trip.dots) {
        if (!dots.has(role)) dots.set(role, []);
        dots.get(role).push(travel);
      }
    }
    const add = (role, body) => {
      const name = `art-${flow.name}-${role}`;
      keyframes.push(`@keyframes ${name} {\n${body}\n}`);
      bindings.push(`  .art-flow-${flow.name} .art-${role} { animation: ${name} ${flow.length}s linear infinite; }`);
    };
    for (const [role, intervals] of boxes) add(role, boxKeyframes(role, intervals, flow.length));
    for (const [role, travels] of dots) add(role, dotKeyframes(travels, flow.length));
  }
  return [
    BEGIN,
    ...keyframes,
    '@media (prefers-reduced-motion: no-preference) {',
    '  .art .art-dot { display: block; }',
    ...bindings,
    '}',
    END,
  ].join('\n');
}

export const BEGIN = '/* BEGIN generated by scripts/site-art.mjs. Edit the timelines there, then run it. */';
export const END = '/* END generated */';

export function replaceBlock(css, block = artCss()) {
  const start = css.indexOf(BEGIN);
  const end = css.indexOf(END);
  if (start < 0 || end < start) throw new Error('site.css has no generated illustration block');
  return css.slice(0, start) + block + css.slice(end + END.length);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const file = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../site/site.css');
  writeFileSync(file, replaceBlock(readFileSync(file, 'utf8')));
  console.log('Updated the illustration animations in site/site.css');
}
