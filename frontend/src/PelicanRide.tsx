import { useEffect, useRef, useState } from "react";

/**
 * 登录页左侧：海岸骑行鹈鹕（SVG + rAF 驱动）。
 * 双腿是两段式 IK、辐条/牙盘/路面/前景花草全部逐帧推进，
 * 附带暂停与变速小控件。
 */
export function PelicanRide() {
  const [paused, setPaused] = useState(false);
  const [speed, setSpeed] = useState(1);
  const state = useRef({ paused: false, speed: 1, elapsed: 0, last: null as number | null });

  useEffect(() => {
    const svgNS = "http://www.w3.org/2000/svg";
    const byId = (id: string) => document.getElementById(id);
    const marks = Array.from({ length: 12 }, (_, i) => {
      const path = document.createElementNS(svgNS, "path");
      byId("jr-roadMarks")?.appendChild(path);
      return { path, x: i * 91, y: 503 + (i % 3) * 9, length: 12 + (i % 4) * 9 };
    });
    const plants = Array.from({ length: 7 }, (_, i) => {
      const use = document.createElementNS(svgNS, "use");
      use.setAttribute("href", i % 3 === 0 ? "#jr-flower" : "#jr-grass");
      byId("jr-foreground")?.appendChild(use);
      return { use, x: i * 163, y: 493 + (i % 2) * 27, scale: 0.55 + (i % 3) * 0.17 };
    });

    function leg(side: "near" | "far", phase: number) {
      const x = 550 + 29 * Math.cos(phase);
      const y = 414 + 29 * Math.sin(phase);
      const hip = side === "near" ? { x: 529, y: 303 } : { x: 511, y: 303 };
      const ankle = { x: x - 7, y: y - 8 };
      const dx = ankle.x - hip.x;
      const dy = ankle.y - hip.y;
      const d = Math.hypot(dx, dy);
      const upper = 77;
      const lower = 77;
      const along = (upper * upper - lower * lower + d * d) / (2 * d);
      const bend = Math.sqrt(Math.max(0, upper * upper - along * along));
      const knee = { x: hip.x + (dx * along) / d + (dy * bend) / d, y: hip.y + (dy * along) / d - (dx * bend) / d };
      byId(side === "near" ? "jr-nearLeg" : "jr-farLeg")?.setAttribute(
        "d",
        `M${hip.x} ${hip.y} L${knee.x} ${knee.y} L${ankle.x} ${ankle.y}`,
      );
      byId(side === "near" ? "jr-nearFoot" : "jr-farFoot")?.setAttribute(
        "d",
        `M${ankle.x} ${ankle.y} Q${x} ${y - 3} ${x + 13} ${y - 3}`,
      );
      byId(side === "near" ? "jr-nearPedal" : "jr-farPedal")?.setAttribute("d", `M${x - 10} ${y + 3}h27`);
    }

    function draw() {
      const s = state.current;
      const phase = s.elapsed * 2.9;
      const degrees = (phase * 180) / Math.PI;
      byId("jr-rearSpokes")?.setAttribute("transform", `rotate(${degrees * 1.45})`);
      byId("jr-frontSpokes")?.setAttribute("transform", `rotate(${degrees * 1.45})`);
      byId("jr-crank")?.setAttribute("transform", `rotate(${degrees} 550 414)`);
      byId("jr-rider")?.setAttribute("transform", `translate(0 ${Math.sin(phase * 2) * 1.1})`);
      leg("far", phase + Math.PI);
      leg("near", phase);
      marks.forEach(({ path, x, y, length }) => {
        const p = 70 + ((((x - s.elapsed * 95) % 960) + 960) % 960);
        path.setAttribute("d", `M${p} ${y}h${length}`);
        path.setAttribute("opacity", String(Math.min(1, (p - 70) / 65, (1030 - p) / 65)));
      });
      plants.forEach(({ use, x, y, scale }) => {
        const p = 70 + ((((x - s.elapsed * 95) % 960) + 960) % 960);
        use.setAttribute("transform", `translate(${p} ${y}) scale(${scale})`);
        use.setAttribute("opacity", String(Math.min(1, (p - 70) / 80, (1030 - p) / 80)));
      });
    }

    let raf = 0;
    function tick(now: number) {
      const s = state.current;
      if (s.last !== null && !s.paused) s.elapsed += Math.min((now - s.last) / 1000, 0.05) * s.speed;
      s.last = now;
      if (!s.paused) draw();
      raf = requestAnimationFrame(tick);
    }
    draw();
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, []);

  function togglePaused() {
    setPaused((prev) => {
      state.current.paused = !prev;
      return !prev;
    });
  }

  function cycleSpeed() {
    setSpeed((prev) => {
      const next = prev === 1 ? 1.5 : prev === 1.5 ? 0.65 : 1;
      state.current.speed = next;
      return next;
    });
  }

  return (
    <div className="pelican-pane">
      <svg className="pelican-scene" viewBox="0 0 1100 580" role="img" aria-labelledby="jr-scene-title jr-scene-desc" xmlns="http://www.w3.org/2000/svg">
        <title id="jr-scene-title">鹈鹕骑着橙色自行车，悠闲地穿过海岸公路</title>
        <desc id="jr-scene-desc">白色鹈鹕戴着绿色帽子和橙色围巾，双脚交替踩踏，车轮不停转动。远处有太阳、海浪、帆船和飞鸟，路边花草向后移动。</desc>
        <defs>
          <linearGradient id="jr-sky" x1="0" y1="0" x2="0" y2="1"><stop stopColor="#e8efe7" /><stop offset="1" stopColor="#f6f5e9" /></linearGradient>
          <linearGradient id="jr-sea" x1="0" y1="0" x2="0" y2="1"><stop stopColor="#b5d7cf" /><stop offset="1" stopColor="#dbe8d8" /></linearGradient>
          <linearGradient id="jr-pouch" x1="0" y1="0" x2="0" y2="1"><stop stopColor="#f8c67b" /><stop offset="1" stopColor="#eda268" /></linearGradient>
          <clipPath id="jr-landscape"><path d="M90 488V260C90 114 246 46 412 65c125-63 297-32 397 44 123 2 206 91 201 213v166Z" /></clipPath>
          <g id="jr-grass" fill="none" stroke="#8a9c77" strokeWidth="2.5" strokeLinecap="round"><path d="M0 0q-3-17-11-21M0 0q0-23 8-29M0 0q7-13 15-15" /></g>
          <g id="jr-flower"><path d="M0 0q-3-15 2-29" fill="none" stroke="#8d9e77" strokeWidth="2" /><g fill="#e9b46e"><circle cx="2" cy="-31" r="4" /><circle cx="-2" cy="-27" r="4" /><circle cx="6" cy="-27" r="4" /></g><circle cx="2" cy="-28" r="2.5" fill="#f8e9b8" /></g>
          <g id="jr-spokes" stroke="#92a7a0" strokeWidth="1.4"><path d="M-69 0H69M0-69V69M-49-49 49 49M-49 49 49-49M-26-64 26 64M-64-26 64 26M-64 26 64-26M-26 64 26-64" /></g>
        </defs>
        <g clipPath="url(#jr-landscape)">
          <path fill="url(#jr-sky)" d="M70 30h970v475H70z" />
          <circle cx="803" cy="144" r="50" fill="#efd18d" />
          <circle cx="803" cy="144" r="64" fill="none" stroke="#efd18d" opacity=".24" />
          <g fill="#fffdf4" className="pelican-cloud-a"><path d="M184 162c-5-12 6-24 19-22 3-20 34-26 46-7 19-5 31 10 26 22 15-1 22 8 20 14H179c-2-3 0-6 5-7Z" /></g>
          <g fill="#fffdf4" className="pelican-cloud-b"><path d="M625 109c-2-12 10-20 21-16 9-20 38-14 41 2 17-5 28 8 22 18h-89Z" /></g>
          <path d="M70 322q98-68 193-41t153 14q75-18 127 18t147-17q140-73 221-20t135 33v143H70Z" fill="#c6d9cd" />
          <path d="M70 331q170-15 366 4t305-5q117-20 299 5v124H70Z" fill="url(#jr-sea)" />
          <g stroke="#eff4e8" strokeWidth="2" strokeLinecap="round" opacity=".8"><path d="M146 364h67m18 0h26m478-12h63m16 0h26M340 390h63m391 9h80m-190-25h33M131 413h98m667-38h40" /></g>
          <g transform="translate(889 284)"><path d="M-20 24h48l-8 9h-29Z" fill="#7f9990" /><path d="M2-30v52H-22Z" fill="#fffaf0" /><path d="M6-17v39h20Z" fill="#e8c999" /><path d="M3-34v61" stroke="#6e8983" strokeWidth="1.5" /></g>
          <g className="pelican-bird" fill="none" stroke="#789189" strokeWidth="2.5" strokeLinecap="round"><path d="M344 167q10-9 21 0 8-12 20-8" /></g>
          <g className="pelican-bird pelican-bird-second" fill="none" stroke="#789189" strokeWidth="2" strokeLinecap="round"><path d="M874 219q7-8 15-1 7-8 16-4" /></g>
          <path d="M65 439q84-34 181-11t169 0q105-34 242 3t253-11q67-23 146 5v91H65Z" fill="#e0dfbb" />
          <path d="M68 463q167-24 304-4t321-1q161-24 351-6v65H68Z" fill="#f0e8ce" />
          <g id="jr-distantGrass"><use href="#jr-grass" x="156" y="442" /><use href="#jr-grass" x="830" y="432" /><use href="#jr-grass" x="988" y="455" /><use href="#jr-flower" x="275" y="453" /></g>
        </g>
        <path d="M63 490H1037" stroke="#bdc4ad" strokeWidth="2" strokeLinecap="round" />
        <path d="M110 520h99m688 0h87" stroke="#dfdfca" strokeWidth="2" strokeLinecap="round" />
        <g id="jr-roadMarks" fill="none" stroke="#c9cbb3" strokeWidth="2" strokeLinecap="round"></g>
        <ellipse cx="558" cy="497" rx="186" ry="12" fill="#6a8170" opacity=".10" />
        <g id="jr-rider">
          <g id="jr-rearWheel" transform="translate(431 414)"><circle r="75" fill="#f8f6e9" fillOpacity=".35" stroke="#2d484b" strokeWidth="9" /><circle r="68.5" fill="none" stroke="#e2e5d9" strokeWidth="3" /><g id="jr-rearSpokes"><use href="#jr-spokes" /><path d="M0-68a68 68 0 0 1 22 4" fill="none" stroke="#eeb276" strokeWidth="3" /></g><circle r="7" fill="#e5dfc9" stroke="#2d484b" strokeWidth="3" /></g>
          <g id="jr-frontWheel" transform="translate(665 414)"><circle r="75" fill="#f8f6e9" fillOpacity=".35" stroke="#2d484b" strokeWidth="9" /><circle r="68.5" fill="none" stroke="#e2e5d9" strokeWidth="3" /><g id="jr-frontSpokes"><use href="#jr-spokes" /><path d="M0-68a68 68 0 0 1 22 4" fill="none" stroke="#eeb276" strokeWidth="3" /></g><circle r="7" fill="#e5dfc9" stroke="#2d484b" strokeWidth="3" /></g>
          <path d="M351 399a83 83 0 0 1 160-9M589 385a83 83 0 0 1 155 15" fill="none" stroke="#c5b99a" strokeWidth="4" strokeLinecap="round" />
          <path id="jr-farLeg" fill="none" stroke="#c78951" strokeWidth="11" strokeLinejoin="round" strokeLinecap="round" />
          <path id="jr-farFoot" fill="none" stroke="#c78951" strokeWidth="10" strokeLinecap="round" />
          <path id="jr-farPedal" stroke="#345053" strokeWidth="5" strokeLinecap="round" />
          <path d="m431 414 75-101 44 101H431l-1 0m76-101 120 0-76 101m76-101 39 101" fill="none" stroke="#ba603e" strokeWidth="10" strokeLinejoin="round" strokeLinecap="round" />
          <path d="m431 412 75-101 44 101m-41-97h114l-74 96m78-97 36 97" fill="none" stroke="#ed9460" strokeWidth="5" strokeLinejoin="round" strokeLinecap="round" />
          <path d="m506 313-8-27m128 27-14-40q-3-12 8-14h25" fill="none" stroke="#3e5b5b" strokeWidth="6" strokeLinecap="round" />
          <path d="M477 287q19-5 41-1" fill="none" stroke="#304a4b" strokeWidth="11" strokeLinecap="round" />
          <path d="M637 259h16" stroke="#aa7451" strokeWidth="9" strokeLinecap="round" />
          <path d="M640 265q18 27 9 62" fill="none" stroke="#556d68" strokeWidth="1.7" />
          <circle cx="631" cy="251" r="6" fill="#e5ba6b" stroke="#566e65" strokeWidth="2" />
          <path d="M431 414 550 401a13 13 0 0 1 0 26L431 420Z" fill="none" stroke="#617771" strokeWidth="2" />
          <circle cx="550" cy="414" r="19" fill="#f1e4c7" stroke="#3e5856" strokeWidth="3" />
          <circle cx="550" cy="414" r="12" fill="none" stroke="#b9b99e" strokeWidth="2" />
          <g id="jr-crank"><path d="M550 414h29" stroke="#385252" strokeWidth="5" strokeLinecap="round" /></g>
          <path d="m460 265-50-34 10 36-20-9q17 34 65 37" fill="#e2e6d8" stroke="#3d5756" strokeWidth="2.5" strokeLinejoin="round" />
          <path d="M437 243q18-38 67-38 22 0 45 6 22-13 21-49-3-38 24-47 30-9 45 15 13 21-1 48-14 25-15 58 0 43-33 63-29 22-70 15-55-8-77-33-14-16-6-38Z" fill="#fffdf1" stroke="#3d5756" strokeWidth="3" />
          <path d="M614 178q-21 39-15 62 6 35-31 54-47 27-94 0 72 18 94-28 11-24 7-49 27-10 39-39Z" fill="#e7eadc" />
          <path d="M464 250q34-32 72-12 36 22 73 20 3 12-14 18-35 18-64 9-36 13-64-7" fill="#f0f1e4" stroke="#3d5756" strokeWidth="2.5" strokeLinecap="round" />
          <path d="M470 266q27 12 48 1m-38 14q22 5 38-1m4-24q19 14 38 14" fill="none" stroke="#c2cbbd" strokeWidth="2" strokeLinecap="round" />
          <path d="M604 258q18-1 29 2" fill="none" stroke="#3d5756" strokeWidth="6" strokeLinecap="round" />
          <path className="pelican-scarf-tip" d="M577 206q-32 13-64-1-19-6-40-2l11 10-11 8q34-6 48 6 29 14 57-6Z" fill="#d9794c" stroke="#b85f40" strokeWidth="1.5" />
          <path d="M573 197q18 14 40 7l-2 15q-21 9-42-6Z" fill="#ec9760" stroke="#bd6d47" strokeWidth="2" />
          <path d="m585 207 17 5" stroke="#f7bd80" strokeWidth="3" strokeLinecap="round" />
          <path d="M625 148 750 163q-26 52-69 41-29-7-51-36Z" fill="url(#jr-pouch)" stroke="#6a6250" strokeWidth="2.5" strokeLinejoin="round" />
          <path d="M626 143q60-1 122 16l9 8-131-9Z" fill="#f5c578" stroke="#6a6250" strokeWidth="2.5" strokeLinejoin="round" />
          <path d="M641 164q22 24 45 29" fill="none" stroke="#dc9662" strokeWidth="2" strokeLinecap="round" />
          <path d="m742 163 9 2" stroke="#bc784b" strokeWidth="2" strokeLinecap="round" />
          <ellipse cx="616" cy="142" rx="5" ry="6.5" fill="#2e4545" /><circle cx="618" cy="140" r="1.7" fill="#fffdf1" />
          <path d="M606 128q7-4 12-1" fill="none" stroke="#3d5756" strokeWidth="2.5" strokeLinecap="round" />
          <ellipse cx="615" cy="162" rx="7" ry="4" fill="#eec4a4" opacity=".7" />
          <path d="M578 120q-4-31 25-34 28-2 34 30Z" fill="#6f9180" stroke="#3d5756" strokeWidth="2.5" />
          <path d="M575 119q38-10 71-1l10 7q-47-5-78 3Z" fill="#4e7367" stroke="#3d5756" strokeWidth="2.5" strokeLinejoin="round" />
          <path d="M603 90q-6 11-3 23" fill="none" stroke="#a6baa0" strokeWidth="2" />
          <circle cx="604" cy="86" r="3" fill="#3d5756" />
          <path id="jr-nearLeg" fill="none" stroke="#e5a363" strokeWidth="12" strokeLinejoin="round" strokeLinecap="round" />
          <path id="jr-nearFoot" fill="none" stroke="#e5a363" strokeWidth="11" strokeLinecap="round" />
          <path id="jr-nearPedal" stroke="#345053" strokeWidth="5" strokeLinecap="round" />
          <path d="M515 297q11 9 24 2" fill="none" stroke="#fffdf1" strokeWidth="13" strokeLinecap="round" />
        </g>
        <g className="pelican-breeze" fill="none" stroke="#9bad9e" strokeWidth="2" strokeLinecap="round"><path d="M335 240h50m-72 13h43m-8 14h25" /></g>
        <g className="pelican-breeze pelican-breeze-two" fill="none" stroke="#9bad9e" strokeWidth="2" strokeLinecap="round"><path d="M771 299h36m12 0h13m-72 14h23" /></g>
        <g id="jr-foreground"></g>
        <g transform="translate(907 468)" fill="#667e6d"><path d="M0 0v-69" stroke="#84947a" strokeWidth="4" /><path d="M-29-68h59l12 12-12 12h-59Z" fill="#f2e9cf" stroke="#8e9d82" strokeWidth="2" /><text x="2" y="-52" textAnchor="middle" fontSize="8" fontFamily="Outfit, sans-serif" letterSpacing="1">SEA SIDE</text></g>
        <text x="551" y="556" textAnchor="middle" fill="#94a08f" fontSize="10" letterSpacing="4" fontFamily="Outfit, sans-serif">LESS HURRY, MORE BREEZE.</text>
      </svg>
      <div className="pelican-controls">
        <span className="pelican-status"><i />{paused ? "看看风景" : "正在兜风"}</span>
        <button type="button" onClick={cycleSpeed}>速度 {speed}×</button>
        <button type="button" className="primary" onClick={togglePaused}>{paused ? "▷ 继续兜风" : "Ⅱ 歇一会儿"}</button>
      </div>
    </div>
  );
}
