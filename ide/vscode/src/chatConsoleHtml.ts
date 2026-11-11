// The console's HTML — the panel's markup, styles and script.
//
// It lives in its own module (no `vscode` import) for two reasons: the
// webview is the one piece of the extension no unit test can reach, and the
// preview harness (scripts/preview-console.mjs) has to render it outside the
// editor. "It compiles" is not "it renders".
/** How many recent turns stay expanded; the rest fold into one row. */
export const VISIBLE_TURNS = 2;

// The panel's own action strip (ADR-0044 decision 1).
//
// The icons are inlined instead of loaded from `media/`: the webview has no
// `localResourceRoots`, so an external file 404s — and a 404 icon shows up in
// VS Code as an empty box, which is harder to diagnose than a missing button.
// All of them draw with `currentColor`, so they follow the theme by themselves.

const ICON_DESKTOP = `<svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><rect x="1.8" y="2.8" width="12.4" height="8.4" rx="1.2" fill="none" stroke="currentColor" stroke-width="1.3"/><path d="M5.5 13.5h5" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/></svg>`;

// Conversation history: three lines, the same glyph Warden uses for its
// "conversations" sheet. The list itself is HIDDEN until asked for — a sidebar
// that always draws a topic dropdown plus two buttons above the feed spends
// the narrowest screen in the product on chrome.
const ICON_HISTORY = `<svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><path d="M2.5 4h11M2.5 8h8M2.5 12h5.5" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/></svg>`;

const ICON_PLUS = `<svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><path d="M8 2.8v10.4M2.8 8h10.4" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/></svg>`;

const ICON_GEAR = `<svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><circle cx="8" cy="8" r="2.1" fill="none" stroke="currentColor" stroke-width="1.3"/><path d="M8 1.6v1.7M8 12.7v1.7M1.6 8h1.7M12.7 8h1.7M3.5 3.5l1.2 1.2M11.3 11.3l1.2 1.2M12.5 3.5l-1.2 1.2M4.7 11.3l-1.2 1.2" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/></svg>`;

const KINDS = ["knowledge", "log", "idea", "other"];

/**
 * The brand mark, inlined so the webview needs no resource roots and no
 * image loads. Same construction as build/icon.svg (the brand SSOT):
 * ADR-0052 "The Knot" — one equal-width band threading itself, three
 * filled paths on a solid ink field, no frame. Generated paths must stay
 * byte-identical to the master.
 */
const MARK_SVG = `<svg viewBox="0 0 512 512" width="28" height="28" aria-hidden="true" focusable="false">
  <rect width="512" height="512" fill="#34322D" rx="112"/>
  <g transform="translate(0 0)">
    <path fill="#F7F4EE" fill-rule="nonzero" d="M256.15 157.01L253.87 157.50L251.60 158.02L249.34 158.56L247.08 159.13L244.83 159.71L242.58 160.31L240.34 160.94L238.11 161.59L235.89 162.25L233.67 162.94L231.46 163.65L229.25 164.38L227.05 165.14L224.86 165.91L222.68 166.70L220.51 167.51L218.34 168.34L216.18 169.19L214.03 170.06L211.89 170.95L209.75 171.86L207.62 172.79L205.51 173.74L203.40 174.70L201.30 175.69L199.21 176.70L197.13 177.72L195.06 178.76L193.00 179.83L190.95 180.91L188.91 182.00L186.87 183.12L184.85 184.26L182.84 185.41L180.84 186.58L178.85 187.77L176.87 188.98L174.90 190.20L172.95 191.45L171.00 192.71L169.07 193.98L167.14 195.28L165.23 196.59L163.33 197.92L161.44 199.27L159.57 200.64L157.70 202.02L155.85 203.42L154.02 204.83L152.19 206.27L150.38 207.72L148.58 209.19L146.80 210.67L145.03 212.17L143.27 213.69L141.52 215.22L139.79 216.77L138.08 218.34L136.38 219.92L134.69 221.52L133.02 223.14L131.36 224.77L129.72 226.42L128.09 228.08L126.48 229.76L124.89 231.45L123.31 233.17L121.75 234.89L120.20 236.64L118.68 238.39L117.17 240.17L115.67 241.96L114.19 243.76L112.74 245.58L111.29 247.42L109.87 249.27L108.47 251.14L107.08 253.02L105.72 254.92L104.37 256.83L103.04 258.75L101.74 260.70L100.45 262.65L99.18 264.62L97.94 266.61L96.71 268.61L95.51 270.62L94.33 272.65L93.17 274.69L92.03 276.75L90.92 278.82L89.82 280.90L88.76 283.00L87.71 285.11L86.69 287.24L85.70 289.38L84.73 291.53L83.79 293.70L82.87 295.88L81.98 298.07L81.12 300.27L80.28 302.49L79.47 304.72L78.69 306.96L77.94 309.22L77.22 311.49L76.53 313.76L75.87 316.05L75.24 318.35L74.64 320.67L74.07 322.99L73.54 325.32L73.04 327.67L72.58 330.02L72.15 332.38L71.76 334.76L71.40 337.14L71.08 339.53L70.80 341.93L70.55 344.33L70.35 346.75L70.18 349.17L70.06 351.59L69.98 354.03L69.94 356.46L69.95 358.90L69.99 361.35L70.09 363.80L70.24 366.25L70.42 368.70L70.66 371.16L70.95 373.61L71.29 376.06L71.68 378.51L72.12 380.96L72.62 383.40L73.16 385.83L73.77 388.26L74.43 390.68L75.15 393.09L75.93 395.48L76.77 397.87L77.66 400.23L78.62 402.58L79.64 404.91L80.73 407.22L81.87 409.51L83.08 411.77L84.35 414.00L85.69 416.20L87.10 418.37L88.56 420.50L90.09 422.59L91.69 424.64L93.35 426.64L95.07 428.60L96.85 430.51L98.69 432.37L100.60 434.17L102.56 435.91L104.58 437.59L106.65 439.21L108.78 440.76L110.95 442.25L113.17 443.66L115.44 445.00L117.75 446.27L120.10 447.46L122.48 448.58L124.90 449.62L127.35 450.58L129.82 451.46L132.32 452.26L134.83 452.98L137.37 453.63L139.92 454.19L142.48 454.68L145.05 455.09L147.62 455.42L150.20 455.67L152.78 455.86L155.36 455.96L157.93 456.00L160.50 455.96L163.05 455.86L165.60 455.69L168.14 455.45L170.67 455.15L173.17 454.78L175.67 454.35L178.14 453.87L180.60 453.32L183.04 452.73L185.46 452.07L187.86 451.36L190.24 450.61L192.59 449.80L194.92 448.94L197.23 448.04L199.51 447.10L201.77 446.10L204.01 445.07L206.22 443.99L208.41 442.88L210.57 441.72L212.70 440.53L214.81 439.31L216.89 438.04L218.95 436.74L220.98 435.41L222.99 434.05L224.97 432.66L226.92 431.23L228.85 429.78L230.75 428.30L232.62 426.79L234.47 425.25L236.30 423.69L238.10 422.10L239.87 420.49L241.61 418.86L243.33 417.20L245.03 415.52L246.70 413.81L248.34 412.09L249.96 410.35L251.56 408.58L253.13 406.80L254.67 404.99L256.19 403.17L257.68 401.33L259.15 399.48L260.60 397.61L262.02 395.72L263.41 393.81L264.79 391.89L266.13 389.96L267.46 388.01L268.76 386.04L270.03 384.06L271.29 382.07L272.52 380.07L273.72 378.05L274.90 376.02L276.06 373.98L277.20 371.93L278.31 369.86L279.40 367.79L280.46 365.70L281.50 363.61L282.52 361.50L283.52 359.38L255.07 355.51L254.11 357.32L253.14 359.12L252.15 360.91L251.13 362.69L250.10 364.45L249.05 366.21L247.98 367.95L246.89 369.67L245.78 371.38L244.66 373.08L243.51 374.76L242.35 376.43L241.16 378.08L239.96 379.72L238.74 381.34L237.49 382.94L236.23 384.53L234.96 386.09L233.66 387.64L232.34 389.17L231.00 390.68L229.65 392.18L228.28 393.65L226.89 395.10L225.48 396.53L224.05 397.94L222.61 399.33L221.15 400.69L219.66 402.03L218.17 403.35L216.65 404.64L215.12 405.90L213.57 407.14L212.00 408.36L210.42 409.55L208.82 410.70L207.20 411.83L205.57 412.93L203.93 414.01L202.27 415.05L200.59 416.05L198.90 417.03L197.20 417.98L195.48 418.89L193.75 419.76L192.01 420.60L190.26 421.40L188.49 422.17L186.72 422.90L184.93 423.59L183.14 424.24L181.34 424.85L179.53 425.42L177.71 425.95L175.89 426.43L174.06 426.87L172.23 427.27L170.39 427.61L168.56 427.92L166.72 428.18L164.88 428.39L163.05 428.55L161.21 428.67L159.38 428.73L157.56 428.75L155.74 428.71L153.93 428.63L152.13 428.49L150.34 428.30L148.56 428.06L146.79 427.77L145.04 427.43L143.31 427.03L141.59 426.58L139.89 426.08L138.21 425.53L136.55 424.92L134.91 424.27L133.30 423.56L131.71 422.81L130.15 422.00L128.61 421.14L127.10 420.24L125.63 419.28L124.18 418.28L122.77 417.23L121.38 416.14L120.03 414.99L118.72 413.81L117.44 412.58L116.20 411.32L114.99 410.00L113.82 408.66L112.69 407.27L111.59 405.84L110.54 404.38L109.53 402.89L108.55 401.36L107.62 399.80L106.72 398.21L105.87 396.59L105.05 394.94L104.28 393.26L103.55 391.57L102.86 389.84L102.21 388.10L101.60 386.33L101.03 384.55L100.51 382.74L100.02 380.92L99.57 379.08L99.16 377.23L98.79 375.36L98.46 373.48L98.17 371.59L97.92 369.69L97.71 367.77L97.53 365.85L97.39 363.92L97.29 361.99L97.22 360.04L97.19 358.10L97.19 356.14L97.23 354.19L97.31 352.23L97.42 350.27L97.56 348.31L97.73 346.34L97.94 344.38L98.18 342.41L98.45 340.45L98.76 338.49L99.09 336.53L99.45 334.57L99.85 332.61L100.27 330.66L100.72 328.71L101.21 326.77L101.72 324.83L102.25 322.90L102.82 320.97L103.41 319.04L104.03 317.12L104.67 315.21L105.34 313.31L106.04 311.41L106.76 309.52L107.50 307.64L108.27 305.76L109.07 303.89L109.88 302.03L110.73 300.18L111.59 298.34L112.48 296.51L113.38 294.68L114.31 292.87L115.27 291.07L116.24 289.27L117.23 287.49L118.25 285.71L119.28 283.95L120.34 282.19L121.42 280.45L122.51 278.72L123.63 276.99L124.76 275.28L125.91 273.59L127.08 271.90L128.27 270.22L129.48 268.56L130.70 266.91L131.95 265.27L133.21 263.64L134.48 262.02L135.78 260.42L137.09 258.83L138.41 257.25L139.75 255.68L141.11 254.13L142.49 252.59L143.87 251.06L145.28 249.55L146.70 248.05L148.13 246.56L149.58 245.08L151.05 243.62L152.52 242.18L154.01 240.74L155.52 239.33L157.04 237.92L158.57 236.53L160.12 235.15L161.68 233.79L163.25 232.44L164.84 231.11L166.43 229.79L168.04 228.48L169.66 227.19L171.30 225.92L172.94 224.66L174.60 223.41L176.27 222.18L177.95 220.97L179.64 219.77L181.34 218.58L183.06 217.41L184.78 216.26L186.51 215.12L188.26 214.00L190.01 212.89L191.78 211.80L193.55 210.73L195.33 209.67L197.13 208.62L198.93 207.60L200.74 206.59L202.56 205.59L204.39 204.62L206.23 203.66L208.08 202.71L209.94 201.79L211.80 200.88L213.67 199.98L215.55 199.11L217.44 198.25L219.33 197.41L221.24 196.58L223.15 195.78L225.06 194.99L226.99 194.22L228.92 193.46L230.85 192.73L232.80 192.01L234.75 191.31L236.70 190.63L238.66 189.97L240.63 189.33L242.60 188.70L244.58 188.09L246.57 187.51L248.56 186.94L250.55 186.39L252.55 185.86L254.55 185.34L256.56 184.85L258.57 184.38L260.58 183.93L262.60 183.49L264.62 183.08L266.65 182.69Z"/>
    <path fill="#F7F4EE" fill-rule="nonzero" d="M293.76 333.28L294.46 331.06L295.15 328.84L295.81 326.60L296.46 324.37L297.08 322.13L297.68 319.88L298.25 317.63L298.81 315.37L299.34 313.11L299.86 310.84L300.35 308.57L300.82 306.30L301.26 304.02L301.69 301.74L302.10 299.45L302.48 297.16L302.85 294.87L303.19 292.57L303.51 290.28L303.81 287.98L304.09 285.67L304.35 283.37L304.59 281.06L304.80 278.75L305.00 276.44L305.17 274.13L305.33 271.81L305.46 269.50L305.57 267.18L305.66 264.87L305.73 262.55L305.78 260.23L305.81 257.91L305.81 255.59L305.80 253.27L305.77 250.96L305.71 248.64L305.63 246.32L305.53 244.00L305.41 241.69L305.27 239.37L305.11 237.06L304.93 234.75L304.73 232.44L304.51 230.13L304.26 227.82L304.00 225.52L303.71 223.22L303.40 220.92L303.07 218.62L302.72 216.33L302.35 214.03L301.96 211.75L301.54 209.46L301.11 207.18L300.65 204.90L300.18 202.63L299.68 200.36L299.16 198.10L298.61 195.84L298.05 193.58L297.47 191.33L296.86 189.09L296.23 186.85L295.58 184.61L294.91 182.38L294.22 180.16L293.50 177.94L292.77 175.73L292.01 173.53L291.23 171.34L290.42 169.15L289.60 166.97L288.75 164.79L287.88 162.63L286.99 160.47L286.08 158.32L285.14 156.18L284.18 154.05L283.20 151.93L282.19 149.81L281.17 147.71L280.12 145.62L279.04 143.54L277.95 141.46L276.83 139.40L275.69 137.35L274.52 135.32L273.33 133.29L272.12 131.28L270.88 129.28L269.62 127.29L268.34 125.32L267.03 123.36L265.70 121.41L264.34 119.48L262.96 117.57L261.56 115.67L260.13 113.78L258.68 111.91L257.20 110.06L255.70 108.23L254.17 106.42L252.62 104.62L251.04 102.84L249.44 101.09L247.81 99.35L246.16 97.63L244.48 95.93L242.78 94.26L241.05 92.61L239.29 90.98L237.51 89.38L235.71 87.80L233.87 86.25L232.02 84.72L230.13 83.22L228.22 81.74L226.29 80.30L224.32 78.89L222.34 77.50L220.32 76.15L218.28 74.83L216.22 73.54L214.13 72.29L212.01 71.08L209.87 69.89L207.70 68.75L205.50 67.65L203.28 66.59L201.04 65.57L198.77 64.59L196.48 63.66L194.17 62.77L191.83 61.93L189.46 61.14L187.08 60.40L184.68 59.71L182.25 59.07L179.81 58.49L177.34 57.97L174.86 57.50L172.36 57.09L169.84 56.75L167.32 56.47L164.77 56.25L162.22 56.10L159.66 56.02L157.09 56.01L154.52 56.06L151.94 56.20L149.36 56.40L146.78 56.68L144.21 57.03L141.64 57.47L139.09 57.98L136.54 58.57L134.01 59.24L131.50 59.99L129.01 60.82L126.55 61.73L124.11 62.71L121.70 63.78L119.33 64.92L116.99 66.13L114.70 67.43L112.45 68.79L110.24 70.23L108.08 71.73L105.97 73.31L103.92 74.95L101.92 76.65L99.97 78.41L98.09 80.23L96.26 82.10L94.50 84.03L92.80 86.00L91.16 88.02L89.58 90.09L88.08 92.19L86.63 94.34L85.25 96.51L83.93 98.72L82.68 100.97L81.49 103.23L80.37 105.53L79.30 107.84L78.30 110.18L77.37 112.54L76.49 114.91L75.67 117.30L74.91 119.70L74.21 122.11L73.57 124.53L72.98 126.96L72.45 129.40L71.97 131.84L71.55 134.29L71.17 136.74L70.85 139.19L70.58 141.64L70.36 144.10L70.18 146.55L70.06 149.00L69.98 151.45L69.94 153.89L69.95 156.33L70.00 158.77L70.10 161.20L70.23 163.62L70.41 166.04L70.63 168.45L70.88 170.86L71.18 173.25L71.51 175.64L71.88 178.02L72.29 180.39L72.73 182.75L73.20 185.10L73.71 187.44L74.26 189.77L74.83 192.09L75.44 194.40L76.08 196.69L76.75 198.98L77.45 201.25L78.18 203.52L78.94 205.77L79.73 208.01L80.55 210.23L81.40 212.45L82.27 214.65L83.17 216.83L84.09 219.01L85.05 221.17L86.02 223.32L87.02 225.45L88.05 227.58L89.10 229.68L90.18 231.78L91.28 233.86L92.40 235.92L93.54 237.98L94.71 240.01L95.90 242.04L97.11 244.05L98.34 246.04L99.59 248.02L100.86 249.99L102.16 251.94L103.47 253.88L104.81 255.80L122.39 233.09L121.30 231.36L120.22 229.62L119.17 227.86L118.14 226.10L117.12 224.32L116.13 222.53L115.16 220.74L114.21 218.93L113.28 217.12L112.38 215.29L111.49 213.46L110.63 211.61L109.79 209.76L108.98 207.90L108.19 206.03L107.42 204.16L106.68 202.27L105.96 200.38L105.27 198.48L104.60 196.58L103.96 194.67L103.34 192.75L102.75 190.82L102.19 188.89L101.66 186.96L101.15 185.02L100.67 183.07L100.22 181.12L99.80 179.17L99.41 177.22L99.05 175.26L98.72 173.30L98.42 171.34L98.15 169.37L97.92 167.41L97.71 165.44L97.54 163.48L97.40 161.52L97.30 159.56L97.23 157.60L97.19 155.64L97.19 153.69L97.23 151.74L97.29 149.80L97.40 147.87L97.55 145.94L97.73 144.02L97.95 142.11L98.20 140.21L98.50 138.32L98.83 136.44L99.21 134.57L99.62 132.72L100.07 130.88L100.56 129.06L101.09 127.26L101.67 125.47L102.28 123.71L102.93 121.97L103.63 120.25L104.37 118.55L105.14 116.88L105.96 115.24L106.82 113.62L107.72 112.03L108.66 110.47L109.64 108.95L110.65 107.46L111.71 106.00L112.81 104.58L113.95 103.20L115.12 101.85L116.33 100.54L117.58 99.28L118.86 98.06L120.18 96.88L121.53 95.74L122.92 94.65L124.34 93.61L125.79 92.61L127.27 91.66L128.78 90.76L130.32 89.91L131.88 89.11L133.47 88.36L135.09 87.66L136.73 87.01L138.39 86.41L140.07 85.86L141.77 85.37L143.49 84.92L145.23 84.53L146.99 84.19L148.75 83.91L150.53 83.67L152.33 83.49L154.13 83.36L155.94 83.28L157.76 83.25L159.58 83.27L161.41 83.34L163.25 83.46L165.08 83.63L166.92 83.85L168.76 84.11L170.59 84.42L172.43 84.77L174.26 85.18L176.09 85.62L177.91 86.11L179.72 86.64L181.53 87.22L183.34 87.83L185.13 88.49L186.91 89.18L188.69 89.91L190.45 90.68L192.20 91.49L193.94 92.33L195.67 93.21L197.39 94.13L199.09 95.07L200.78 96.05L202.45 97.06L204.11 98.11L205.75 99.18L207.38 100.29L209.00 101.42L210.59 102.58L212.17 103.77L213.74 104.99L215.29 106.23L216.82 107.50L218.33 108.80L219.83 110.11L221.31 111.46L222.77 112.82L224.21 114.21L225.63 115.63L227.04 117.06L228.43 118.51L229.80 119.99L231.15 121.48L232.49 122.99L233.80 124.53L235.10 126.08L236.37 127.65L237.63 129.24L238.87 130.84L240.09 132.46L241.29 134.10L242.47 135.75L243.64 137.42L244.78 139.10L245.91 140.80L247.01 142.52L248.10 144.24L249.17 145.98L250.22 147.74L251.25 149.50L252.26 151.28L253.25 153.07L254.22 154.88L255.17 156.69L256.11 158.52L257.02 160.35L257.92 162.20L258.79 164.06L259.65 165.93L260.49 167.81L261.30 169.69L262.10 171.59L262.88 173.49L263.65 175.41L264.39 177.33L265.11 179.26L265.81 181.20L266.50 183.14L267.16 185.09L267.81 187.05L268.44 189.02L269.05 190.99L269.64 192.97L270.21 194.96L270.76 196.95L271.30 198.95L271.81 200.95L272.31 202.96L272.78 204.97L273.24 206.99L273.68 209.02L274.09 211.04L274.49 213.07L274.88 215.11L275.24 217.15L275.58 219.19L275.91 221.24L276.21 223.29L276.50 225.34L276.77 227.40L277.02 229.45L277.25 231.51L277.47 233.58L277.66 235.64L277.83 237.71L277.99 239.78L278.13 241.85L278.25 243.92L278.34 245.99L278.43 248.06L278.49 250.14L278.53 252.21L278.56 254.28L278.56 256.36L278.55 258.43L278.52 260.51L278.47 262.58L278.40 264.65L278.31 266.73L278.21 268.80L278.08 270.87L277.94 272.94L277.77 275.01L277.59 277.07L277.39 279.14L277.18 281.20L276.94 283.26L276.68 285.31L276.41 287.37L276.11 289.42L275.80 291.47L275.47 293.51L275.12 295.55L274.75 297.59L274.36 299.63L273.95 301.66L273.53 303.68L273.08 305.70L272.62 307.72L272.13 309.73L271.63 311.74L271.11 313.74L270.57 315.74L270.02 317.73L269.44 319.71L268.84 321.69L268.23 323.66L267.59 325.62L266.94 327.58L266.27 329.53Z"/>
    <path fill="#F7F4EE" fill-rule="nonzero" d="M122.30 277.71L123.86 279.44L125.45 281.14L127.05 282.83L128.66 284.51L130.29 286.16L131.94 287.81L133.60 289.43L135.28 291.04L136.97 292.64L138.68 294.21L140.40 295.77L142.13 297.32L143.88 298.85L145.65 300.36L147.42 301.85L149.21 303.33L151.02 304.79L152.83 306.24L154.66 307.66L156.50 309.07L158.36 310.47L160.22 311.85L162.10 313.20L164.00 314.55L165.90 315.87L167.82 317.18L169.74 318.47L171.68 319.74L173.63 320.99L175.59 322.23L177.56 323.45L179.55 324.65L181.54 325.83L183.55 327.00L185.56 328.15L187.58 329.28L189.62 330.39L191.67 331.48L193.72 332.55L195.79 333.60L197.86 334.64L199.94 335.66L202.04 336.66L204.14 337.64L206.25 338.60L208.37 339.54L210.50 340.46L212.64 341.36L214.78 342.25L216.94 343.11L219.10 343.96L221.27 344.78L223.45 345.58L225.63 346.37L227.82 347.13L230.02 347.87L232.23 348.60L234.45 349.30L236.67 349.98L238.90 350.64L241.13 351.28L243.37 351.90L245.62 352.50L247.87 353.08L250.13 353.63L252.40 354.16L254.67 354.67L256.95 355.16L259.23 355.63L261.52 356.07L263.81 356.50L266.10 356.89L268.41 357.27L270.71 357.62L273.02 357.95L275.34 358.26L277.66 358.54L279.98 358.80L282.30 359.04L284.63 359.25L286.96 359.43L289.30 359.59L291.64 359.73L293.98 359.84L296.32 359.93L298.66 359.99L301.01 360.03L303.36 360.03L305.70 360.02L308.05 359.97L310.40 359.90L312.75 359.81L315.11 359.68L317.46 359.53L319.81 359.35L322.16 359.14L324.51 358.90L326.85 358.64L329.20 358.34L331.54 358.02L333.88 357.66L336.22 357.28L338.56 356.86L340.89 356.42L343.22 355.94L345.54 355.43L347.86 354.89L350.17 354.32L352.48 353.71L354.78 353.07L357.08 352.40L359.36 351.70L361.64 350.96L363.91 350.18L366.17 349.37L368.43 348.53L370.67 347.65L372.90 346.73L375.12 345.77L377.32 344.78L379.51 343.75L381.69 342.68L383.86 341.58L386.00 340.43L388.13 339.24L390.24 338.02L392.34 336.76L394.41 335.45L396.46 334.10L398.49 332.71L400.50 331.28L402.48 329.80L404.43 328.28L406.36 326.72L408.26 325.11L410.12 323.46L411.96 321.77L413.75 320.03L415.52 318.25L417.25 316.42L418.93 314.55L420.58 312.63L422.18 310.67L423.73 308.67L425.24 306.62L426.70 304.53L428.11 302.39L429.46 300.21L430.75 297.99L431.99 295.74L433.16 293.44L434.28 291.10L435.32 288.73L436.30 286.33L437.21 283.88L438.05 281.42L438.81 278.92L439.49 276.39L440.10 273.84L440.63 271.27L441.07 268.68L441.44 266.08L441.72 263.46L441.92 260.84L442.03 258.21L442.06 255.57L442.00 252.94L441.86 250.31L441.64 247.68L441.33 245.07L440.94 242.47L440.46 239.89L439.91 237.33L439.28 234.78L438.57 232.27L437.78 229.78L436.92 227.32L435.99 224.89L434.99 222.49L433.92 220.13L432.79 217.81L431.59 215.52L430.34 213.28L429.02 211.07L427.65 208.91L426.23 206.79L424.76 204.71L423.23 202.67L421.66 200.68L420.05 198.74L418.39 196.84L416.69 194.98L414.95 193.16L413.17 191.40L411.36 189.67L409.52 187.99L407.64 186.36L405.73 184.77L403.80 183.22L401.84 181.72L399.85 180.26L397.83 178.84L395.80 177.46L393.74 176.12L391.66 174.83L389.56 173.58L387.44 172.36L385.31 171.19L383.15 170.06L380.99 168.97L378.80 167.91L376.61 166.89L374.40 165.91L372.17 164.97L369.94 164.06L367.69 163.19L365.44 162.36L363.18 161.56L360.90 160.80L358.62 160.07L356.33 159.38L354.03 158.72L351.73 158.09L349.42 157.49L347.11 156.93L344.79 156.40L342.46 155.90L340.13 155.43L337.80 155.00L335.46 154.59L333.12 154.22L330.78 153.88L328.44 153.56L326.09 153.28L323.74 153.02L321.39 152.79L319.04 152.59L316.69 152.42L314.34 152.28L311.99 152.16L309.64 152.07L307.29 152.01L304.94 151.97L302.59 151.96L300.24 151.98L297.90 152.03L295.56 152.09L293.21 152.19L290.87 152.31L288.54 152.45L286.20 152.62L283.87 152.82L294.74 179.40L296.79 179.32L298.84 179.26L300.88 179.23L302.93 179.21L304.97 179.23L307.02 179.26L309.06 179.32L311.10 179.40L313.13 179.50L315.17 179.63L317.20 179.78L319.22 179.96L321.25 180.16L323.26 180.38L325.28 180.63L327.29 180.90L329.29 181.20L331.29 181.52L333.28 181.87L335.26 182.25L337.24 182.65L339.21 183.08L341.17 183.53L343.12 184.01L345.06 184.51L347.00 185.04L348.92 185.60L350.83 186.18L352.73 186.80L354.62 187.44L356.50 188.10L358.36 188.80L360.21 189.52L362.05 190.27L363.86 191.05L365.67 191.85L367.45 192.69L369.22 193.55L370.97 194.44L372.71 195.35L374.42 196.30L376.11 197.28L377.78 198.28L379.42 199.31L381.04 200.37L382.64 201.46L384.22 202.58L385.76 203.72L387.28 204.90L388.77 206.10L390.23 207.32L391.66 208.58L393.06 209.86L394.42 211.17L395.75 212.51L397.05 213.87L398.31 215.26L399.53 216.67L400.71 218.11L401.85 219.57L402.95 221.06L404.01 222.57L405.03 224.10L406.00 225.65L406.92 227.22L407.80 228.81L408.63 230.43L409.42 232.05L410.15 233.70L410.83 235.36L411.46 237.04L412.04 238.72L412.56 240.43L413.03 242.14L413.45 243.86L413.81 245.59L414.12 247.33L414.37 249.08L414.57 250.83L414.71 252.58L414.79 254.34L414.81 256.10L414.78 257.85L414.69 259.61L414.55 261.36L414.35 263.11L414.09 264.86L413.78 266.60L413.41 268.33L412.98 270.05L412.51 271.76L411.98 273.46L411.39 275.15L410.76 276.82L410.07 278.48L409.33 280.12L408.54 281.75L407.71 283.36L406.82 284.95L405.89 286.52L404.92 288.07L403.90 289.60L402.83 291.10L401.73 292.59L400.58 294.05L399.39 295.48L398.17 296.89L396.91 298.28L395.61 299.64L394.27 300.97L392.90 302.28L391.50 303.56L390.07 304.81L388.61 306.04L387.11 307.23L385.59 308.40L384.04 309.54L382.47 310.66L380.87 311.74L379.24 312.80L377.59 313.83L375.92 314.83L374.23 315.80L372.52 316.75L370.78 317.66L369.03 318.55L367.26 319.41L365.47 320.24L363.67 321.04L361.85 321.81L360.01 322.56L358.16 323.28L356.29 323.97L354.42 324.63L352.53 325.27L350.62 325.88L348.71 326.46L346.79 327.02L344.85 327.54L342.91 328.05L340.95 328.52L338.99 328.97L337.02 329.40L335.05 329.80L333.06 330.17L331.07 330.51L329.07 330.83L327.07 331.13L325.06 331.40L323.04 331.65L321.02 331.87L319.00 332.06L316.97 332.24L314.94 332.39L312.91 332.51L310.87 332.61L308.83 332.69L306.79 332.75L304.75 332.78L302.71 332.79L300.66 332.77L298.61 332.73L296.57 332.67L294.52 332.59L292.47 332.49L290.42 332.36L288.37 332.21L286.33 332.04L284.28 331.85L282.24 331.64L280.19 331.40L278.15 331.15L276.11 330.87L274.07 330.57L272.04 330.25L270.00 329.92L267.97 329.56L265.95 329.18L263.92 328.78L261.90 328.36L259.89 327.92L257.87 327.46L255.86 326.98L253.86 326.48L251.86 325.96L249.86 325.43L247.87 324.87L245.88 324.30L243.90 323.70L241.92 323.09L239.95 322.45L237.99 321.80L236.03 321.13L234.07 320.45L232.13 319.74L230.18 319.02L228.25 318.28L226.32 317.52L224.40 316.74L222.49 315.95L220.58 315.13L218.68 314.30L216.79 313.46L214.90 312.59L213.02 311.71L211.16 310.81L209.29 309.89L207.44 308.96L205.60 308.01L203.76 307.05L201.93 306.06L200.12 305.06L198.31 304.05L196.51 303.02L194.72 301.97L192.94 300.91L191.17 299.83L189.40 298.73L187.65 297.62L185.91 296.49L184.18 295.34L182.46 294.18L180.75 293.01L179.06 291.82L177.37 290.61L175.69 289.39L174.03 288.16L172.37 286.91L170.73 285.64L169.10 284.36L167.48 283.07L165.88 281.76L164.29 280.43L162.71 279.09L161.14 277.74L159.58 276.37L158.04 274.99L156.51 273.60L155.00 272.19L153.50 270.76L152.01 269.32L150.54 267.87L149.08 266.41L147.64 264.93L146.21 263.44L144.79 261.93L143.39 260.41L142.01 258.88L140.64 257.34L139.29 255.78Z"/>
  </g>
</svg>`;

export function consoleHtml(nonce: string): string {
  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';">
<title>Cinder Chat</title><style>
  :root {
    --r-border: var(--vscode-panel-border, var(--vscode-contrastBorder, #444));
    --r-muted: var(--vscode-descriptionForeground, #888);
    --r-accent: var(--vscode-focusBorder, #06c);
  }
  * { box-sizing: border-box; }
  html, body { height: 100%; }
  body {
    margin: 0; position: relative; display: flex; flex-direction: column; overflow: hidden;
    font-family: var(--vscode-font-family, sans-serif);
    font-size: var(--vscode-font-size, 13px);
    color: var(--vscode-foreground, #333);
    background: var(--vscode-sideBar-background, #fff);
  }
  button { font-family: var(--vscode-font-family, sans-serif); }
  /* ---------- header: identity + the panel's OWN action strip ---------- */
  /* The strip used to live in VS Code's view/title, where the host drew it
     at the far right of the sidebar title bar and cropped the third button
     off the panel's edge. The webview cannot draw that strip, so the actions
     moved in here (ADR-0044 decision 1).

     The conversation controls are ICONS here, and the list itself is a sheet
     that only exists while it is open: the old always-on topic row (dropdown
     + "new" + refresh + a status line) spent the top of a 300px sidebar on
     chrome and left almost nothing for the conversation. */
  header.bar {
    display: flex; align-items: center; gap: 8px; flex: none;
    padding: 6px 8px 6px 10px; border-bottom: 1px solid var(--r-border);
  }
  .mark { flex: none; display: block; border-radius: 3px; }
  .wordmark {
    flex: 1; min-width: 0; font-size: 13px; font-weight: 700; letter-spacing: 0.02em;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .actions { display: flex; align-items: center; gap: 1px; flex: none; }
  .icon-btn {
    flex: none; width: 24px; height: 24px; cursor: pointer; position: relative;
    display: flex; align-items: center; justify-content: center;
    background: none; border: none; border-radius: 4px;
    color: var(--vscode-foreground, #333);
  }
  .icon-btn:hover { background: var(--vscode-toolbar-hoverBackground, rgba(128,128,128,.18)); }
  .icon-btn:focus-visible { outline: 1px solid var(--r-accent); outline-offset: -1px; }
  .icon-btn svg { width: 16px; height: 16px; display: block; }
  /* A problem in the shared conversation shows as a dot on the history button:
     the status line lives in the sheet, but its absence must not hide a fault. */
  .icon-btn.dot::after {
    content: ""; position: absolute; top: 3px; right: 3px; width: 5px; height: 5px;
    border-radius: 50%; background: var(--vscode-editorWarning-foreground, #d18616);
  }
  /* A narrow sidebar has no room for the wordmark; the icons never shrink. */
  @media (max-width: 260px) { .wordmark { display: none; } }
  /* ---------- announcement bar (ADR-0036 decision 5) ---------- */
  .announce {
    flex: none; display: flex; align-items: flex-start; gap: 8px;
    padding: 7px 10px; font-size: 12px; line-height: 1.45;
    border-bottom: 1px solid var(--r-border);
    background: var(--vscode-inputValidation-infoBackground, rgba(0,120,215,.12));
  }
  .announce[hidden] { display: none; }
  .announce.warn { background: var(--vscode-inputValidation-warningBackground, rgba(255,185,0,.14)); }
  .announce.error { background: var(--vscode-inputValidation-errorBackground, rgba(255,0,0,.12)); }
  .announce .msg { flex: 1; min-width: 0; overflow-wrap: anywhere; }
  .announce .act { flex: none; }
  .announce .act button {
    padding: 2px 7px; font-size: 11px; cursor: pointer; border-radius: 4px;
    color: var(--vscode-button-foreground, #fff);
    background: var(--vscode-button-background, #06c); border: none;
  }
  .announce .act button:hover { filter: brightness(1.1); }
  .announce .x {
    flex: none; background: none; border: none; cursor: pointer;
    color: inherit; font-size: 14px; line-height: 1; padding: 0 2px; opacity: .7;
  }
  .announce .x:hover { opacity: 1; }
  /* ---------- the feed: the only scroll area ---------- */
  main.feed {
    flex: 1; min-height: 0; overflow-y: auto; overflow-x: hidden;
    padding: 10px 12px 18px;
  }
  .turn { margin-bottom: 16px; }
  .turn-head { display: flex; align-items: flex-start; gap: 6px; }
  .turn-head .mode {
    flex: none; font-size: 9px; font-weight: 700; letter-spacing: .06em;
    text-transform: uppercase; padding: 1px 5px; border-radius: 4px;
    border: 1px solid currentColor; color: var(--r-muted);
  }
  .turn-head .q {
    flex: 1; min-width: 0; font-size: 12px; font-weight: 600; line-height: 1.45;
    overflow-wrap: anywhere;
  }
  .turn-head .chev {
    flex: none; background: none; border: none; cursor: pointer;
    color: var(--r-muted); font-size: 12px; line-height: 1.3; padding: 0 2px;
  }
  .turn-head .chev:hover { color: var(--vscode-foreground, #333); }
  .turn-body { margin-top: 6px; padding-left: 2px; }
  .pending { font-size: 12px; color: var(--r-muted); font-style: italic; }
  .turn-error {
    font-size: 12px; line-height: 1.5; padding: 7px 9px;
    border: 1px solid var(--vscode-inputValidation-errorBorder, #c33);
    border-radius: 5px; overflow-wrap: anywhere;
    background: var(--vscode-inputValidation-errorBackground, rgba(255,0,0,.08));
  }
  .answer { font-size: 13px; line-height: 1.65; overflow-wrap: anywhere; }
  .answer pre.raw {
    margin: 0; padding: 9px 11px; white-space: pre-wrap; overflow-wrap: anywhere;
    border: 1px dashed var(--r-border); border-radius: 5px;
    font-family: var(--vscode-editor-font-family, monospace); font-size: 12px;
  }
  /* the shared markdown renderer (note bodies AND answers) */
  .answer pre, .detail-body pre {
    margin: 10px 0; padding: 10px 12px; overflow-x: auto;
    background: var(--vscode-textCodeBlock-background, rgba(128,128,128,.12));
    border-radius: 6px; font-family: var(--vscode-editor-font-family, monospace);
    font-size: 12px;
  }
  .answer code, .detail-body code {
    padding: 1px 4px; border-radius: 3px;
    background: var(--vscode-textCodeBlock-background, rgba(128,128,128,.12));
    font-family: var(--vscode-editor-font-family, monospace); font-size: 12px;
  }
  .answer pre code, .detail-body pre code { padding: 0; background: none; }
  .answer h3, .answer h4, .detail-body h3, .detail-body h4 { margin: 14px 0 6px; }
  .answer ul, .answer ol, .detail-body ul, .detail-body ol { margin: 8px 0; padding-left: 22px; }
  .answer a, .detail-body a { color: var(--vscode-textLink-foreground, #06c); }
  .diagram {
    margin: 10px 0; border: 1px solid var(--r-border); border-radius: 6px; overflow: hidden;
  }
  .diagram-head {
    padding: 4px 10px; font-size: 10px; font-weight: 600; letter-spacing: .04em;
    text-transform: uppercase; color: var(--r-muted);
    background: var(--vscode-textCodeBlock-background, rgba(128,128,128,.12));
  }
  .diagram pre { margin: 0; border-radius: 0; }
  .diagram-note {
    padding: 4px 10px; font-size: 10px; color: var(--r-muted);
    border-top: 1px solid var(--r-border);
  }
  /* ---------- sources: the footnote of an answer ---------- */
  .sources-toggle {
    margin-top: 8px; padding: 2px 0; background: none; border: none; cursor: pointer;
    font-size: 11px; color: var(--r-muted);
  }
  .sources-toggle:hover { color: var(--vscode-foreground, #333); }
  .sources {
    margin-top: 4px; padding-left: 8px; border-left: 2px solid var(--r-border);
  }
  .src-item { font-size: 11px; line-height: 1.5; margin-bottom: 5px; overflow-wrap: anywhere; }
  .src-item .ref { font-weight: 700; font-family: var(--vscode-editor-font-family, monospace); }
  .src-item .stype { font-weight: 600; }
  .src-item .smeta { color: var(--r-muted); }
  /* ---------- cards ---------- */
  .cards { display: flex; flex-direction: column; gap: 6px; }
  .card { border: 1px solid var(--r-border); border-radius: 6px; padding: 7px 9px; }
  .card:hover { border-color: var(--r-accent); }
  .card-row { display: flex; align-items: baseline; gap: 6px; cursor: pointer; }
  .badge {
    flex: none; font-size: 9px; font-weight: 600; letter-spacing: 0.04em;
    text-transform: uppercase; padding: 1px 5px; border-radius: 4px;
    border: 1px solid currentColor;
  }
  .badge.note { color: var(--vscode-textLink-foreground, #06c); }
  .badge.todo { color: var(--vscode-editorWarning-foreground, #b90); }
  .badge.wiki, .badge.page { color: var(--vscode-charts-purple, #8250df); }
  .card-title {
    flex: 1; min-width: 0; font-size: 12px; font-weight: 600; line-height: 1.4;
    overflow-wrap: anywhere;
  }
  .card-meta { flex: none; font-size: 10px; color: var(--r-muted); }
  .card-snippet {
    margin-top: 3px; font-size: 11px; line-height: 1.45; color: var(--r-muted);
    display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden;
  }
  .card-detail { margin-top: 7px; padding-top: 7px; border-top: 1px dashed var(--r-border); }
  .card-detail:empty { display: none; }
  .detail-title { font-size: 13px; font-weight: 700; margin-bottom: 4px; overflow-wrap: anywhere; }
  .detail-meta {
    display: flex; flex-wrap: wrap; align-items: center; gap: 6px;
    font-size: 10px; color: var(--r-muted); margin-bottom: 8px;
  }
  .detail-body { font-size: 12px; line-height: 1.6; overflow-wrap: anywhere; }
  .card-actions { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 8px; }
  .card-actions button, .editor .actions button {
    padding: 3px 9px; font-size: 11px; cursor: pointer;
    color: var(--vscode-button-foreground, #fff);
    background: var(--vscode-button-background, #06c);
    border: none; border-radius: 4px;
  }
  .card-actions button.secondary, .editor .actions button.secondary {
    color: var(--vscode-foreground, #333);
    background: var(--vscode-button-secondaryBackground, rgba(128,128,128,.18));
  }
  .card-actions button:hover, .editor .actions button:hover { filter: brightness(1.12); }
  .card-actions button:disabled, .editor .actions button:disabled { opacity: .55; cursor: default; filter: none; }
  .scope-note { margin-top: 6px; font-size: 10px; line-height: 1.5; color: var(--r-muted); }
  /* ---------- editor (edit / new / AI proposal), inside a card ---------- */
  .editor { display: flex; flex-direction: column; gap: 7px; }
  .editor .banner {
    padding: 6px 8px; font-size: 11px; line-height: 1.5;
    border: 1px solid var(--r-accent); border-radius: 5px;
    background: rgba(0,120,215,.08);
  }
  .editor .banner em { display: block; margin-top: 3px; font-style: normal; color: var(--r-muted); }
  .editor .err {
    padding: 6px 8px; font-size: 11px;
    border: 1px solid var(--vscode-inputValidation-errorBorder, #c33); border-radius: 5px;
    background: var(--vscode-inputValidation-errorBackground, rgba(255,0,0,.08));
  }
  .editor .err[hidden] { display: none; }
  .editor input[type=text], .editor select, .editor textarea {
    width: 100%; padding: 4px 7px; font-size: 11px;
    font-family: var(--vscode-font-family, sans-serif);
    color: var(--vscode-input-foreground, #333);
    background: var(--vscode-input-background, #fff);
    border: 1px solid var(--vscode-input-border, #ccc);
    border-radius: 4px; outline: none;
  }
  .editor input:focus, .editor select:focus, .editor textarea:focus { border-color: var(--r-accent); }
  .editor textarea {
    min-height: 120px; resize: vertical; line-height: 1.5;
    font-family: var(--vscode-editor-font-family, monospace);
  }
  .editor .row { display: flex; gap: 6px; }
  .editor .row select { flex: 1; min-width: 0; }
  .editor .label { font-size: 9px; font-weight: 600; letter-spacing: .05em; text-transform: uppercase; color: var(--r-muted); }
  .editor .preview {
    padding: 8px 10px; font-size: 12px; line-height: 1.6;
    border: 1px dashed var(--r-border); border-radius: 5px; overflow-wrap: anywhere;
  }
  /* ---------- the composer (always there) ---------- */
  footer.composer {
    flex: none; position: relative;
    border-top: 1px solid var(--r-border); padding: 8px 10px 8px;
  }
  .input-wrap { position: relative; }
  .composer textarea {
    display: block; width: 100%; min-height: 32px; max-height: 190px; resize: none;
    padding: 6px 30px 6px 8px; font-size: 12px; line-height: 1.5;
    font-family: var(--vscode-font-family, sans-serif);
    color: var(--vscode-input-foreground, #333);
    background: var(--vscode-input-background, #fff);
    border: 1px solid var(--vscode-input-border, #ccc); border-radius: 5px; outline: none;
  }
  .composer textarea:focus { border-color: var(--r-accent); }
  /* The send key sits INSIDE the input box (Windsurf's shape), so the input
     grows downward and never covers it. */
  .send {
    position: absolute; right: 3px; bottom: 3px;
    width: 24px; height: 24px; padding: 0; font-size: 12px; line-height: 1; cursor: pointer;
    display: flex; align-items: center; justify-content: center;
    border: none; border-radius: 4px;
    color: var(--vscode-button-foreground, #fff);
    background: var(--vscode-button-background, #06c);
    transition: background 120ms ease, opacity 120ms ease;
  }
  .send:hover:not(:disabled) { background: var(--vscode-button-hoverBackground, var(--vscode-button-background, #06c)); filter: brightness(.94); }
  .send:active:not(:disabled) { filter: brightness(.88); }
  .send:focus-visible { outline: 1px solid var(--vscode-focusBorder, #06c); outline-offset: 1px; }
  .send:disabled { opacity: .5; cursor: default; }
  /* The tool row: the mode group on the left, the project chip on the right.
     It wraps instead of clipping: a narrow side bar used to hide the chip
     beyond overflow:hidden, which made project scope unreachable. */
  .tools { display: flex; align-items: center; flex-wrap: wrap; gap: 6px 6px; margin-top: 7px; }
  .modes { display: flex; gap: 3px; flex: none; flex-wrap: wrap; }
  .mode-btn {
    padding: 4px 8px; min-height: 24px; font-size: 11px; cursor: pointer; border-radius: 6px;
    border: 1px solid var(--r-border);
    color: var(--r-muted); background: transparent;
    transition: color 120ms ease, border-color 120ms ease;
  }
  .mode-btn:hover { color: var(--vscode-foreground, #333); }
  .mode-btn:active { color: var(--vscode-foreground, #333); border-color: var(--vscode-foreground, #333); }
  .mode-btn:focus-visible { outline: 1px solid var(--vscode-focusBorder, #06c); outline-offset: 1px; }
  .mode-btn.active {
    color: var(--vscode-button-foreground, #fff);
    background: var(--vscode-button-background, #06c); border-color: transparent;
  }
  .chip {
    margin-left: auto; flex: none; max-width: 100%; min-height: 24px; overflow: hidden;
    text-overflow: ellipsis; white-space: nowrap;
    padding: 4px 9px; font-size: 11px; cursor: pointer; border-radius: 6px;
    border: 1px solid var(--r-border); background: transparent; color: var(--r-muted);
    transition: color 120ms ease, border-color 120ms ease;
  }
  .chip:active { color: var(--vscode-foreground, #333); }
  .chip:focus-visible { outline: 1px solid var(--vscode-focusBorder, #06c); outline-offset: 1px; }
  .chip.locked { color: var(--vscode-foreground, #333); border-color: var(--r-accent); }
  .chip.pick { color: var(--vscode-foreground, #333); border-style: dashed; }
  /* ---------- the project picker (a popover under the chip) ---------- */
  /* Anchored to the composer's top edge, not a magic pixel offset: the input
     grows with the question, and a fixed bottom: 46px used to end up underneath the
     textarea and the send key. */
  .pop {
    position: absolute; z-index: 5; left: 10px; right: 10px; bottom: 100%;
    margin-bottom: 6px;
    max-height: 220px; overflow-y: auto;
    border: 1px solid var(--r-border); border-radius: 6px;
    background: var(--vscode-dropdown-background, var(--vscode-editorWidget-background, #fff));
    box-shadow: 0 4px 14px rgba(0,0,0,.22);
  }
  .pop[hidden] { display: none; }
  .pop-item {
    display: block; width: 100%; text-align: left;
    padding: 6px 10px; font-size: 12px; cursor: pointer;
    background: none; border: none; color: var(--vscode-foreground, #333);
  }
  .pop-item:hover { background: var(--vscode-list-hoverBackground, rgba(128,128,128,.18)); }
  .pop-empty { padding: 8px 10px; font-size: 11px; color: var(--r-muted); }
  button:focus-visible, select:focus-visible, input:focus-visible, textarea:focus-visible { outline: 2px solid var(--vscode-focusBorder, currentColor); outline-offset: 2px; }
  /* ---------- sheets: the conversation history and the AI provider -------- */
  /* Both are the panel's own pages, drawn over the conversation. They exist
     only while open — the Warden app's shape, and the reason the top of the
     sidebar no longer carries a permanent dropdown plus two buttons. */
  .sheet {
    position: absolute; inset: 0; z-index: 10; display: flex; flex-direction: column;
    background: var(--vscode-sideBar-background, #fff);
  }
  .sheet[hidden] { display: none; }
  .sheet-head {
    flex: none; display: flex; align-items: center; gap: 8px;
    padding: 8px 10px; border-bottom: 1px solid var(--r-border);
  }
  .sheet-title { flex: 1; min-width: 0; font-size: 12px; font-weight: 700; letter-spacing: .02em; }
  .sheet-body { flex: 1; min-height: 0; overflow-y: auto; padding: 10px; }
  .sheet-actions { display: flex; gap: 6px; margin-bottom: 8px; }
  .sheet-actions button, .btn {
    padding: 4px 9px; font-size: 11px; cursor: pointer; border-radius: 4px;
    border: 1px solid var(--r-border); background: transparent;
    color: var(--vscode-foreground, #333);
  }
  .btn.primary, .sheet-actions button.primary {
    color: var(--vscode-button-foreground, #fff);
    background: var(--vscode-button-background, #06c); border-color: transparent;
  }
  .btn:hover, .sheet-actions button:hover { filter: brightness(1.1); }
  .btn:disabled, .sheet-actions button:disabled { opacity: .5; cursor: default; filter: none; }
  .topic-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 2px; }
  .topic-item {
    display: block; width: 100%; text-align: left; padding: 7px 9px; cursor: pointer;
    background: none; border: 1px solid transparent; border-radius: 5px;
    color: var(--vscode-foreground, #333); font-size: 12px;
  }
  .topic-item:hover { background: var(--vscode-list-hoverBackground, rgba(128,128,128,.16)); }
  .topic-item.active { border-color: var(--r-accent); }
  .topic-item .t-title { display: block; font-weight: 600; overflow-wrap: anywhere; }
  .topic-item .t-sub { display: block; margin-top: 2px; font-size: 10px; color: var(--r-muted); overflow-wrap: anywhere; }
  .topic-status { margin: 10px 0 0; font-size: 10px; line-height: 1.5; color: var(--r-muted); overflow-wrap: anywhere; }
  .topic-status.error { color: var(--vscode-errorForeground, #c33); }
  #topic-form { margin-bottom: 8px; display: flex; flex-direction: column; gap: 6px; }
  #topic-form[hidden] { display: none; }
  .field { display: flex; flex-direction: column; gap: 3px; margin-bottom: 9px; }
  .field .flabel { font-size: 10px; font-weight: 600; letter-spacing: .04em; text-transform: uppercase; color: var(--r-muted); }
  .field input, #topic-title {
    width: 100%; padding: 5px 8px; font-size: 12px;
    font-family: var(--vscode-font-family, sans-serif);
    color: var(--vscode-input-foreground, #333);
    background: var(--vscode-input-background, #fff);
    border: 1px solid var(--vscode-input-border, #ccc); border-radius: 4px; outline: none;
  }
  .field input:focus, #topic-title:focus { border-color: var(--r-accent); }
  .sheet-hint { margin: 0 0 10px; font-size: 11px; line-height: 1.5; color: var(--r-muted); }
  .form-status { margin: 8px 0 0; font-size: 11px; line-height: 1.5; overflow-wrap: anywhere; }
  .form-status.ok { color: var(--vscode-testing-iconPassed, #1a7f37); }
  .form-status.err { color: var(--vscode-errorForeground, #c33); }
  /* ---------- the empty state ---------- */
  .empty { padding: 18px 12px; font-size: 12px; line-height: 1.6; color: var(--r-muted); }
  .empty h2 { margin: 0 0 8px; font-size: 14px; font-weight: 700; color: var(--vscode-foreground, #333); }
  .empty p { margin: 0 0 8px; }
  .empty kbd {
    padding: 1px 5px; font-size: 10px; border-radius: 3px;
    font-family: var(--vscode-editor-font-family, monospace);
    border: 1px solid var(--r-border);
  }
  .examples { list-style: none; margin: 0; padding: 0; }
  .examples li { margin-bottom: 5px; }
  .examples button {
    width: 100%; text-align: left; padding: 6px 9px; font-size: 12px; cursor: pointer;
    border: 1px solid var(--r-border); border-radius: 5px;
    color: var(--vscode-foreground, #333); background: transparent;
  }
  .examples button:hover { border-color: var(--r-accent); }
</style>
</head>
<body>
<header class="bar">
  <span class="mark">${MARK_SVG}</span>
  <span class="wordmark">Cinder</span>
  <span class="actions">
    <button class="icon-btn" id="history-open" type="button" title="Conversation history" aria-label="Conversation history" aria-haspopup="dialog" aria-controls="history-sheet">${ICON_HISTORY}</button>
    <button class="icon-btn" id="topic-new" type="button" title="New conversation" aria-label="New conversation">${ICON_PLUS}</button>
    <button class="icon-btn" id="provider-open" type="button" title="AI provider settings" aria-label="AI provider settings" aria-haspopup="dialog" aria-controls="provider-sheet">${ICON_GEAR}</button>
    <button class="icon-btn" id="act-desktop" type="button" title="Open the Cinder Desktop app" aria-label="Open the Cinder Desktop app">${ICON_DESKTOP}</button>
  </span>
</header>
<div class="announce" id="announce" hidden>
  <span class="msg" id="announce-msg"></span>
  <span class="act" id="announce-act"></span>
  <button class="x" id="announce-x" type="button" title="Dismiss" aria-label="Dismiss">✕</button>
</div>
<main class="feed" id="feed"></main>
<footer class="composer">
  <div class="pop" id="project-pop" hidden></div>
  <div class="input-wrap">
    <textarea id="q" rows="1" placeholder="Ask about this project…" spellcheck="false"></textarea>
    <button class="send" id="send" type="button" title="Send (Enter)" aria-label="Send">↵</button>
  </div>
  <div class="tools">
    <span class="modes" role="group" aria-label="Knowledge mode">
      <button class="mode-btn active" data-mode="ask" type="button">Ask</button>
      <button class="mode-btn" data-mode="notes" type="button">Notes</button>
      <button class="mode-btn" data-mode="pages" type="button">Pages</button>
    </span>
    <button class="chip" id="chip" type="button">All projects</button>
  </div>
</footer>

<!-- The conversation history: opened from the header icon, closed with ✕/Esc. -->
<section class="sheet" id="history-sheet" role="dialog" aria-modal="true" aria-labelledby="history-title" hidden>
  <header class="sheet-head">
    <span class="sheet-title" id="history-title">Conversations</span>
    <button class="icon-btn" type="button" data-close="history-sheet" aria-label="Close conversation history" title="Close">✕</button>
  </header>
  <div class="sheet-body">
    <div class="sheet-actions">
      <button class="primary" id="topic-new-in-sheet" type="button">＋ New conversation</button>
      <button id="topic-refresh" type="button" title="Refresh shared conversations">↻ Refresh</button>
    </div>
    <form id="topic-form" hidden>
      <input id="topic-title" aria-label="Conversation title" placeholder="Name this conversation" maxlength="160" required>
      <div class="sheet-actions">
        <button class="primary" type="submit">Create</button>
        <button id="topic-cancel" type="button">Cancel</button>
      </div>
    </form>
    <ul class="topic-list" id="topic-list" aria-label="Conversation topic"></ul>
    <p class="topic-status" id="topic-status" role="status">Shared through Cinder · connect Cinder MCP to sync</p>
  </div>
</section>

<!-- The AI provider: the entry the panel used to lack entirely. A 404 from
     repotory_ask was a dead end without it. -->
<section class="sheet" id="provider-sheet" role="dialog" aria-modal="true" aria-labelledby="provider-title" hidden>
  <header class="sheet-head">
    <span class="sheet-title" id="provider-title">AI provider</span>
    <button class="icon-btn" type="button" data-close="provider-sheet" aria-label="Close AI provider settings" title="Close">✕</button>
  </header>
  <div class="sheet-body">
    <p class="sheet-hint">Any OpenAI-compatible endpoint: OpenRouter, OpenAI, DeepSeek, vLLM, LM Studio, Ollama. Answers are written by this endpoint; the key stays on this machine. Fetching models and testing the typed values connect directly; saving — and testing the saved key — need the local Cinder service (start the desktop app).</p>
    <label class="field">
      <span class="flabel">Base URL</span>
      <input id="p-base" type="text" placeholder="https://api.deepseek.com  or  http://localhost:1234/v1" spellcheck="false" autocomplete="off">
    </label>
    <label class="field">
      <span class="flabel">Model</span>
      <input id="p-model" type="text" placeholder="deepseek-chat / gpt-4o-mini / …" list="p-model-list" spellcheck="false" autocomplete="off">
      <datalist id="p-model-list"></datalist>
    </label>
    <label class="field">
      <span class="flabel">API key</span>
      <input id="p-key" type="password" placeholder="Optional for local endpoints" autocomplete="off">
    </label>
    <p class="sheet-hint" id="p-saved">No key saved.</p>
    <div class="sheet-actions">
      <button id="p-fetch" type="button">Fetch models</button>
      <button id="p-test" type="button">Test</button>
      <button class="primary" id="p-save" type="button">Save</button>
    </div>
    <p class="form-status" id="p-status" role="status"></p>
  </div>
</section>

<script nonce="${nonce}">
const MARK_SVG = ${JSON.stringify(MARK_SVG)};
const KINDS = ${JSON.stringify(KINDS)};
const VISIBLE_TURNS = ${VISIBLE_TURNS};
const vscode = acquireVsCodeApi();
const feedEl = document.getElementById('feed');
const announceEl = document.getElementById('announce');
const announceMsg = document.getElementById('announce-msg');
const announceAct = document.getElementById('announce-act');
const q = document.getElementById('q');
const sendBtn = document.getElementById('send');
const chip = document.getElementById('chip');
const modeBtns = Array.from(document.querySelectorAll('.mode-btn'));
const popEl = document.getElementById('project-pop');
const topicList = document.getElementById('topic-list');
const topicStatus = document.getElementById('topic-status');
const historyBtn = document.getElementById('history-open');
const providerStatus = document.getElementById('p-status');

// ---- state -----------------------------------------------------------------
// The host owns the DATA (the turns); this side owns the FOLD STATE. A
// collapsed turn must stay collapsed while other turns arrive.
let mode = 'ask';
let scope = { projectId: 0, projectName: '', repository: '', locked: false };
let turns = [];
let sharedThreadId = null;
let sharedBusy = false;
let sharedSignature = '';
let threads = [];
const drafts = new Map();
let topicSignature = '';
let collapsedTurns = new Set();
let expandedSources = new Set();
let expandedCards = new Set();
let showEarlier = false;
let announcement = null;
// The projects the workspace could belong to (ADR-0044 decision 6). The host
// sends them when the workspace resolves to none.
let candidates = [];
// The open editor: { turnId, cardId, node }. The node is kept alive across
// re-renders so a half-typed draft survives an unrelated turn update.
let editor = null;
// Whether a key is stored server-side (the value itself never reaches here).
let providerHasKey = false;

const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => (
  { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
));

// Minimal markdown: the note store is markdown, and a wall of raw asterisks
// undoes the point of a reader. Anything unknown is escaped and shown
// verbatim — never dropped.
function md(src) {
  const blocks = [];
  let text = String(src ?? '');
  text = text.replace(/\`\`\`([^\\n]*)\\n([\\s\\S]*?)\`\`\`/g, (_, lang, code) => {
    if (lang.trim().toLowerCase() === 'mermaid') {
      blocks.push('<div class="diagram"><div class="diagram-head">Flowchart · mermaid source</div>' +
        '<pre>' + esc(code.replace(/^\\n/, '')) + '</pre>' +
        '<div class="diagram-note">Renders as a diagram in the Cinder Desktop app; the source stays editable here.</div></div>');
    } else {
      blocks.push('<pre><code>' + esc((lang ? lang + '\\n' : '') + code.replace(/^\\n/, '')) + '</code></pre>');
    }
    return '\\u0000' + (blocks.length - 1) + '\\u0000';
  });
  const inline = (s) => esc(s)
    .replace(/\`([^\`\\n]+)\`/g, '<code>$1</code>')
    .replace(/\\*\\*([^*\\n]+)\\*\\*/g, '<strong>$1</strong>')
    .replace(/\\[([^\\]]+)\\]\\(([^)\\s]+)\\)/g, '<a href="$2">$1</a>');
  const out = [];
  let list = null;
  for (const para of text.split(/\\n{2,}/)) {
    const p = para.trim();
    if (!p) continue;
    if (/^\\u0000\\d+\\u0000$/.test(p)) { out.push(blocks[+p.slice(1, -1)]); list = null; continue; }
    const lines = p.split('\\n');
    let buffer = [];
    const flush = () => { if (buffer.length) { out.push('<p>' + buffer.map(inline).join('<br>') + '</p>'); buffer = []; } };
    for (const line of lines) {
      const h = line.match(/^(#{1,4})\\s+(.*)$/);
      const ul = line.match(/^[-*]\\s+(.*)$/);
      const ol = line.match(/^\\d+[.)]\\s+(.*)$/);
      if (h) { flush(); out.push('<h' + (h[1].length + 2) + '>' + inline(h[2]) + '</h' + (h[1].length + 2) + '>'); }
      else if (ul) { flush(); if (list !== 'ul') { if (list) out.push('</' + list + '>'); out.push('<ul>'); list = 'ul'; } out.push('<li>' + inline(ul[1]) + '</li>'); }
      else if (ol) { flush(); if (list !== 'ol') { if (list) out.push('</' + list + '>'); out.push('<ol>'); list = 'ol'; } out.push('<li>' + inline(ol[1]) + '</li>'); }
      else { if (list) { out.push('</' + list + '>'); list = null; } buffer.push(line); }
    }
    flush();
    if (list) { out.push('</' + list + '>'); list = null; }
  }
  return out.join('\\n');
}

function badge(text, cls) {
  return '<span class="badge ' + esc(cls || text) + '">' + esc(text) + '</span>';
}

function cardBadge(card) {
  if (card.type === 'wiki') return badge(card.pageKind || 'wiki', 'wiki');
  return badge(card.type, card.type);
}

// ---- sheets -----------------------------------------------------------------
function openSheet(id) {
  for (const el of Array.from(document.querySelectorAll('.sheet'))) el.hidden = true;
  const sheet = document.getElementById(id);
  if (!sheet) return;
  sheet.hidden = false;
  const focusable = sheet.querySelector('input');
  if (focusable) focusable.focus();
}

function closeSheets() {
  for (const el of Array.from(document.querySelectorAll('.sheet'))) el.hidden = true;
}

document.addEventListener('keydown', (ev) => {
  if (ev.key === 'Escape') closeSheets();
});
for (const btn of Array.from(document.querySelectorAll('[data-close]'))) {
  btn.addEventListener('click', () => closeSheets());
}

// ---- the conversation history -----------------------------------------------
// Local order: the local console first (this panel's own turns), then the
// conversations shared through the Cinder MCP service, newest first.
function renderTopics() {
  topicList.replaceChildren();
  const local = document.createElement('button');
  local.className = 'topic-item' + (sharedThreadId ? '' : ' active');
  local.type = 'button';
  const localTitle = document.createElement('span');
  localTitle.className = 't-title';
  localTitle.textContent = 'Local knowledge console';
  const localSub = document.createElement('span');
  localSub.className = 't-sub';
  localSub.textContent = 'This panel only · not shared';
  local.append(localTitle, localSub);
  local.addEventListener('click', () => {
    closeSheets();
    vscode.postMessage({ type: 'sharedSelect', threadId: null });
  });
  topicList.appendChild(local);

  const sorted = threads.slice().sort((a, b) => {
    const at = a.updatedAt || '';
    const bt = b.updatedAt || '';
    return at < bt ? 1 : at > bt ? -1 : 0;
  });
  for (const thread of sorted) {
    const item = document.createElement('button');
    item.className = 'topic-item' + (thread.id === sharedThreadId ? ' active' : '');
    item.type = 'button';
    const title = document.createElement('span');
    title.className = 't-title';
    title.textContent = thread.title || '(untitled)';
    const sub = document.createElement('span');
    sub.className = 't-sub';
    sub.textContent = thread.path || 'All projects';
    item.append(title, sub);
    item.addEventListener('click', () => {
      closeSheets();
      vscode.postMessage({ type: 'sharedSelect', threadId: thread.id });
    });
    topicList.appendChild(item);
  }
  if (!sorted.length) {
    const empty = document.createElement('li');
    empty.className = 'topic-status';
    empty.textContent = 'No shared conversations yet — the local console works without them.';
    topicList.appendChild(empty);
  }
}

// ---- scope chip ------------------------------------------------------------
// The chip is never disabled. An unresolvable workspace used to disable it,
// which left the reader with nothing to do (ADR-0044 decision 6); now it
// opens the project picker instead.
function renderScope() {
  if (!scope.projectId) {
    chip.textContent = candidates.length ? 'Choose project' : 'All projects';
    chip.className = 'chip pick';
    chip.title = 'This workspace does not resolve to a Cinder project yet — click to pick one.';
    return;
  }
  if (scope.locked) {
    chip.textContent = '🔒 ' + scope.projectName;
    chip.className = 'chip locked';
    chip.title = 'Locked to ' + scope.projectName + ' — click to search across all projects.';
  } else {
    chip.textContent = 'All projects';
    chip.className = 'chip';
    chip.title = 'Searching across all projects — click to lock back to ' + scope.projectName + '.';
  }
}

// ---- the project picker -----------------------------------------------------
function closePop() {
  popEl.hidden = true;
  popEl.innerHTML = '';
}

function renderPop() {
  popEl.innerHTML = '';
  const all = document.createElement('button');
  all.className = 'pop-item';
  all.type = 'button';
  all.textContent = 'All projects';
  all.addEventListener('click', () => {
    closePop();
    if (scope.projectId) vscode.postMessage({ type: 'scopeToggle' });
  });
  popEl.appendChild(all);
  for (const c of candidates) {
    const item = document.createElement('button');
    item.className = 'pop-item';
    item.type = 'button';
    item.textContent = c.name;
    item.addEventListener('click', () => {
      closePop();
      vscode.postMessage({ type: 'pickProject', projectId: c.id });
    });
    popEl.appendChild(item);
  }
  if (!candidates.length) {
    const empty = document.createElement('div');
    empty.className = 'pop-empty';
    empty.textContent = 'No projects in the knowledge base yet — run a scan in the desktop app.';
    popEl.appendChild(empty);
  }
  popEl.hidden = false;
}

// ---- announcement bar ------------------------------------------------------
function renderAnnounce() {
  if (!announcement) {
    announceEl.hidden = true;
    announceAct.replaceChildren();
    return;
  }
  announceEl.hidden = false;
  announceEl.className = 'announce ' + announcement.level;
  announceMsg.textContent = announcement.message;
  announceAct.replaceChildren();
  if (announcement.action) {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.textContent = announcement.action.label;
    btn.addEventListener('click', () => {
      openSheet('provider-sheet');
      vscode.postMessage({ type: 'providerLoad' });
    });
    announceAct.appendChild(btn);
  }
}

// ---- the feed --------------------------------------------------------------
function renderFeed() {
  const stick = feedEl.scrollHeight - feedEl.scrollTop - feedEl.clientHeight < 48;
  const top = feedEl.scrollTop;
  feedEl.innerHTML = '';
  if (!turns.length) {
    const empty = document.createElement('div');
    empty.className = 'empty';
    // Copy only talks about actions (ADR-0044 decision 4): one question and
    // three tappable examples. The brand mark is NOT repeated here — the
    // header already carries it, and a second logo right under the first was
    // the redundancy the panel review called out.
    empty.innerHTML =
      '<h2>Ask what your project knows.</h2>' +
      '<p>Cinder answers from the notes your coding agents left behind, with the sources listed.</p>' +
      '<ul class="examples">' +
      '<li><button type="button" data-example="ask">Why was the retry budget raised?</button></li>' +
      '<li><button type="button" data-example="notes">Search the notes &amp; todos</button></li>' +
      '<li><button type="button" data-example="pages">Browse the approved knowledge pages</button></li>' +
      '</ul>';
    for (const btn of Array.from(empty.querySelectorAll('[data-example]'))) {
      btn.addEventListener('click', () => {
        const kind = btn.getAttribute('data-example');
        if (kind === 'notes' || kind === 'pages') {
          setMode(kind);
          vscode.postMessage({ type: 'mode', mode: kind });
          q.focus();
          return;
        }
        q.focus();
      });
    }
    feedEl.appendChild(empty);
    return;
  }
  const hidden = Math.max(0, turns.length - VISIBLE_TURNS);
  if (hidden > 0) {
    const fold = document.createElement('button');
    fold.className = 'earlier';
    fold.type = 'button';
    fold.textContent = (showEarlier ? '▾ ' : '▸ ') + hidden + ' earlier turn' + (hidden === 1 ? '' : 's');
    fold.title = showEarlier ? 'Collapse the earlier turns' : 'Show the earlier turns';
    fold.addEventListener('click', () => { showEarlier = !showEarlier; renderFeed(); });
    feedEl.appendChild(fold);
  }
  const visible = showEarlier ? turns : turns.slice(-VISIBLE_TURNS);
  for (const turn of visible) feedEl.appendChild(renderTurn(turn));
  // The feed is rebuilt from scratch, so the scroll position has to be put
  // back: a reader who scrolled up to re-read an old answer must not be
  // yanked to the bottom by the next turn arriving.
  feedEl.scrollTop = stick ? feedEl.scrollHeight : top;
}

function renderTurn(turn) {
  const el = document.createElement('article');
  el.className = 'turn';
  el.dataset.id = turn.id;
  const collapsed = collapsedTurns.has(turn.id);
  const head = document.createElement('div');
  head.className = 'turn-head';
  const modeLabel = turn.mode === 'ask' ? 'Ask' : (turn.mode === 'notes' ? 'Notes' : 'Pages');
  head.innerHTML = '<span class="mode">' + esc(modeLabel) + '</span>' +
    '<span class="q">' + esc(turn.query) + '</span>';
  const chev = document.createElement('button');
  chev.className = 'chev';
  chev.type = 'button';
  chev.textContent = collapsed ? '▸' : '▾';
  chev.title = collapsed ? 'Expand this turn' : 'Collapse this turn';
  chev.addEventListener('click', () => {
    if (collapsedTurns.has(turn.id)) collapsedTurns.delete(turn.id);
    else collapsedTurns.add(turn.id);
    renderFeed();
  });
  head.appendChild(chev);
  el.appendChild(head);
  const body = document.createElement('div');
  body.className = 'turn-body';
  if (!collapsed) body.appendChild(renderTurnBody(turn));
  el.appendChild(body);
  return el;
}

function renderTurnBody(turn) {
  const frag = document.createDocumentFragment();
  if (turn.state === 'pending') {
    const p = document.createElement('div');
    p.className = 'pending';
    p.textContent = 'Working…';
    frag.appendChild(p);
    return frag;
  }
  if (turn.state === 'error') {
    const e = document.createElement('div');
    e.className = 'turn-error';
    e.textContent = turn.error || 'The request failed.';
    frag.appendChild(e);
    return frag;
  }
  if (turn.mode === 'ask') {
    if (turn.verbatim) {
      // The source list could not be parsed: show the tool's text as it came,
      // so nothing is swallowed.
      const pre = document.createElement('pre');
      pre.className = 'raw';
      pre.textContent = turn.answer || '';
      frag.appendChild(pre);
    } else {
      const a = document.createElement('div');
      a.className = 'answer';
      a.innerHTML = md(turn.answer || '');
      frag.appendChild(a);
    }
    const sources = turn.sources || [];
    if (sources.length) {
      const isOpen = expandedSources.has(turn.id);
      const btn = document.createElement('button');
      btn.className = 'sources-toggle';
      btn.type = 'button';
      btn.textContent = 'Sources (' + sources.length + ') ' + (isOpen ? '▾' : '▸');
      btn.addEventListener('click', () => {
        if (isOpen) expandedSources.delete(turn.id);
        else expandedSources.add(turn.id);
        renderFeed();
      });
      frag.appendChild(btn);
      if (isOpen) {
        const list = document.createElement('div');
        list.className = 'sources';
        for (const s of sources) {
          const item = document.createElement('div');
          item.className = 'src-item';
          const meta = [];
          meta.push('id ' + s.id);
          if (s.slug) meta.push('slug ' + s.slug);
          if (s.project) meta.push('project ' + s.project);
          item.innerHTML = '<span class="ref">' + esc('[' + s.ref + ']') + '</span> ' +
            '<span class="stype">' + esc(s.type) + '</span> ' +
            '<span class="title">' + esc(s.title) + '</span> ' +
            '<span class="smeta">(' + esc(meta.join(', ')) + ')</span>';
          list.appendChild(item);
        }
        frag.appendChild(list);
      }
    }
    return frag;
  }
  const cards = turn.cards || [];
  const wrap = document.createElement('div');
  wrap.className = 'cards';
  for (const card of cards) wrap.appendChild(renderCard(turn, card));
  frag.appendChild(wrap);
  if (turn.scopeNote) {
    const note = document.createElement('div');
    note.className = 'scope-note';
    note.textContent = turn.scopeNote;
    frag.appendChild(note);
  }
  return frag;
}

function renderCard(turn, card) {
  const el = document.createElement('div');
  el.className = 'card';
  el.dataset.cardId = String(card.id);
  const row = document.createElement('div');
  row.className = 'card-row';
  row.innerHTML = cardBadge(card) + '<span class="card-title">' + esc(card.title) + '</span>';
  const meta = document.createElement('span');
  meta.className = 'card-meta';
  meta.textContent = '#' + card.id;
  row.appendChild(meta);
  el.appendChild(row);
  if (card.snippet) {
    const sn = document.createElement('div');
    sn.className = 'card-snippet';
    sn.textContent = card.snippet;
    el.appendChild(sn);
  }
  const key = turn.id + '/' + card.type + '/' + card.id;
  const isOpen = expandedCards.has(key);
  const detail = document.createElement('div');
  detail.className = 'card-detail';
  if (isOpen) {
    if (editor && editor.turnId === turn.id && editor.cardType === card.type && editor.cardId === card.id) {
      detail.appendChild(editor.node);
    } else if (card.detail) {
      detail.appendChild(renderDetail(card.detail));
    } else {
      const wait = document.createElement('div');
      wait.className = 'pending';
      wait.textContent = 'Loading…';
      detail.appendChild(wait);
    }
  }
  el.appendChild(detail);
  if (isOpen) el.appendChild(renderCardActions(turn, card));
  row.addEventListener('click', () => {
    if (isOpen) expandedCards.delete(key);
    else {
      expandedCards.add(key);
      vscode.postMessage({ type: 'expandCard', turnId: turn.id, cardType: card.type, cardId: card.id });
    }
    renderFeed();
  });
  return el;
}

function renderDetail(d) {
  const frag = document.createDocumentFragment();
  if (d.source === 'error') {
    // The body could not be read: say so instead of showing an empty pane.
    const e = document.createElement('div');
    e.className = 'turn-error';
    e.textContent = d.body || 'This item could not be read.';
    frag.appendChild(e);
    return frag;
  }
  const title = document.createElement('div');
  title.className = 'detail-title';
  title.textContent = d.title;
  frag.appendChild(title);
  const metaParts = [];
  if (d.type === 'wiki') {
    if (d.slug) metaParts.push(d.slug);
    metaParts.push(d.project_id ? 'project ' + d.project_id : 'global');
  } else if (d.project_id) {
    metaParts.push('project ' + d.project_id);
  }
  if (d.updated_at) metaParts.push('updated ' + d.updated_at.slice(0, 10));
  if (d.tags) metaParts.push(d.tags);
  if (d.source === 'snippet') metaParts.push('excerpt');
  const meta = document.createElement('div');
  meta.className = 'detail-meta';
  meta.innerHTML = cardBadge({ type: d.type, pageKind: d.pageKind }) + '<span>·</span> #' + d.id +
    metaParts.map((m) => '<span>·</span> ' + esc(m)).join('');
  frag.appendChild(meta);
  const body = document.createElement('div');
  body.className = 'detail-body';
  body.innerHTML = d.body ? md(d.body) : '<i>No content returned for this item.</i>';
  frag.appendChild(body);
  return frag;
}

function renderCardActions(turn, card) {
  const bar = document.createElement('div');
  bar.className = 'card-actions';
  const mk = (label, cls, fn) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = cls || '';
    b.textContent = label;
    b.addEventListener('click', (ev) => { ev.stopPropagation(); fn(b); });
    return b;
  };
  bar.appendChild(mk('Open in Cinder', '', () => {
    vscode.postMessage({ type: 'open', turnId: turn.id, card });
  }));
  if (card.type === 'note') {
    bar.appendChild(mk('Edit', 'secondary', () => {
      vscode.postMessage({ type: 'edit', turnId: turn.id, cardType: card.type, cardId: card.id });
    }));
    bar.appendChild(mk('New note here', 'secondary', () => {
      vscode.postMessage({ type: 'create', turnId: turn.id, cardType: card.type, cardId: card.id });
    }));
    bar.appendChild(mk('AI Tidy', 'secondary', (b) => {
      b.disabled = true;
      b.textContent = 'Tidying…';
      vscode.postMessage({ type: 'organize', turnId: turn.id, cardType: card.type, cardId: card.id });
    }));
  }
  return bar;
}

// ---- the editor (inside the card it belongs to) ---------------------------
function kindOptions(selected) {
  return KINDS.map((k) => '<option value="' + k + '"' + (k === selected ? ' selected' : '') + '>' + k + '</option>').join('');
}

function projectOptions(projects, selectedId) {
  return projects.map((p) =>
    '<option value="' + p.id + '"' + (p.id === selectedId ? ' selected' : '') + '>' + esc(p.name) + '</option>').join('');
}

function renderEditor(turnId, cardType, cardId, draft, projects, proposal, warning) {
  const node = document.createElement('div');
  node.className = 'editor';
  const saveLabel = draft.id ? 'Save' : 'Create note';
  const banner = proposal
    ? '<div class="banner">AI tidied draft — review, then save.' +
      (warning ? '<em>' + esc(warning) + '</em>' : '') + '</div>'
    : '';
  const projectField = projects.length
    ? '<span class="label">Project</span><select id="e-project">' + projectOptions(projects, draft.project_id) + '</select>'
    : '<span class="label">Project</span><input type="text" value="' + esc('project ' + draft.project_id) + '" disabled>';
  node.innerHTML = banner +
    '<div class="err" id="e-err" hidden></div>' +
    '<span class="label">Title</span>' +
    '<input type="text" id="e-title" value="' + esc(draft.title) + '" placeholder="note title">' +
    '<div class="row">' +
    '<div style="flex:1"><span class="label">Kind</span><select id="e-kind">' + kindOptions(draft.kind) + '</select></div>' +
    '<div style="flex:2"><span class="label">Tags</span><input type="text" id="e-tags" value="' + esc(draft.tags) + '" placeholder="comma, separated"></div>' +
    '</div>' +
    projectField +
    '<span class="label">Content (Markdown)</span>' +
    '<textarea id="e-content" spellcheck="false">' + esc(draft.content) + '</textarea>' +
    '<div class="preview"><span class="label">Preview</span><div id="e-preview-body">' +
    (draft.content ? md(draft.content) : '<i>Nothing to preview yet.</i>') + '</div></div>' +
    '<div class="actions">' +
    '<button id="e-save">' + esc(saveLabel) + '</button>' +
    '<button id="e-cancel" class="secondary">Cancel</button>' +
    '</div>';

  const content = node.querySelector('#e-content');
  const preview = node.querySelector('#e-preview-body');
  content.addEventListener('input', () => {
    preview.innerHTML = content.value ? md(content.value) : '<i>Nothing to preview yet.</i>';
  });
  node.querySelector('#e-save').addEventListener('click', () => {
    const btn = node.querySelector('#e-save');
    btn.disabled = true;
    btn.textContent = 'Saving…';
    const projectSel = node.querySelector('#e-project');
    vscode.postMessage({
      type: 'save',
      turnId,
      cardType,
      cardId,
      draft: {
        id: draft.id,
        project_id: projectSel ? Number(projectSel.value) : draft.project_id,
        title: node.querySelector('#e-title').value,
        tags: node.querySelector('#e-tags').value,
        kind: node.querySelector('#e-kind').value,
        content: content.value,
      },
    });
  });
  node.querySelector('#e-cancel').addEventListener('click', () => {
    editor = null;
    expandedCards.delete(turnId + '/' + cardType + '/' + cardId);
    vscode.postMessage({ type: 'cancelEdit', turnId, cardType, cardId });
    renderFeed();
  });

  editor = { turnId, cardType, cardId, node, saveLabel };
  expandedCards.add(turnId + '/' + cardType + '/' + cardId);
  renderFeed();
  const title = node.querySelector('#e-title');
  if (title) title.focus();
}

function showEditorError(message) {
  if (!editor) return;
  const err = editor.node.querySelector('#e-err');
  if (err) { err.textContent = message; err.hidden = false; }
  const btn = editor.node.querySelector('#e-save');
  // The draft is kept: the user fixes the transport problem and presses save
  // again, without retyping.
  if (btn) { btn.disabled = false; btn.textContent = editor.saveLabel; }
}

// ---- composer --------------------------------------------------------------
function setMode(next) {
  mode = next;
  for (const b of modeBtns) b.classList.toggle('active', b.dataset.mode === next);
  q.placeholder = next === 'ask'
    ? 'Ask about this project…'
    : (next === 'notes' ? 'Search notes & todos…' : 'Search approved knowledge pages…');
}

function submit() {
  const text = q.value.trim();
  if (!text || (sharedThreadId && sharedBusy)) return;
  if (sharedThreadId) {
    sharedBusy = true;
    sendBtn.disabled = true;
    drafts.set(sharedThreadId, q.value);
  }
  vscode.postMessage({ type: 'submit', query: text, mode: sharedThreadId ? 'ask' : mode });
  if (!sharedThreadId) { q.value = ''; q.style.height = 'auto'; }
}

for (const b of modeBtns) {
  b.addEventListener('click', () => { setMode(b.dataset.mode); vscode.postMessage({ type: 'mode', mode }); });
}

// The chip opens the project picker when the workspace resolves to nothing,
// and toggles the lock when it does (ADR-0044 decision 6).
chip.addEventListener('click', () => {
  if (!scope.projectId) {
    if (popEl.hidden) renderPop();
    else closePop();
    return;
  }
  vscode.postMessage({ type: 'scopeToggle' });
});
sendBtn.addEventListener('click', submit);

// ---- the panel's own action strip -----------------------------------------
document.getElementById('act-desktop').addEventListener('click', () => {
  vscode.postMessage({ type: 'openDesktop' });
});

historyBtn.addEventListener('click', () => {
  renderTopics();
  openSheet('history-sheet');
});
document.getElementById('provider-open').addEventListener('click', () => {
  openSheet('provider-sheet');
  providerStatus.textContent = 'Reading the saved provider…';
  providerStatus.className = 'form-status';
  vscode.postMessage({ type: 'providerLoad' });
});

document.getElementById('announce-x').addEventListener('click', () => {
  announcement = null;
  renderAnnounce();
});

q.addEventListener('input', () => {
  q.style.height = 'auto';
  q.style.height = Math.min(q.scrollHeight, 190) + 'px';
});
q.addEventListener('keydown', (ev) => {
  if (ev.key === 'Enter' && !ev.shiftKey && !ev.isComposing && ev.keyCode !== 229) {
    ev.preventDefault();
    submit();
  }
});

// ---- the history sheet's controls ------------------------------------------
document.getElementById('topic-new').addEventListener('click', () => {
  // The header "+" goes straight to the create form: one click, then the name.
  renderTopics();
  openSheet('history-sheet');
  document.getElementById('topic-form').hidden = false;
  document.getElementById('topic-title').focus();
});
document.getElementById('topic-new-in-sheet').addEventListener('click', () => {
  document.getElementById('topic-form').hidden = false;
  document.getElementById('topic-title').focus();
});
document.getElementById('topic-cancel').addEventListener('click', () => {
  document.getElementById('topic-form').hidden = true;
});
document.getElementById('topic-form').addEventListener('submit', (event) => {
  event.preventDefault();
  const title = document.getElementById('topic-title').value.trim();
  if (title && !sharedBusy) {
    vscode.postMessage({ type: 'sharedCreate', title });
    document.getElementById('topic-form').hidden = true;
    document.getElementById('topic-title').value = '';
  }
});
document.getElementById('topic-refresh').addEventListener('click', () => vscode.postMessage({ type: 'sharedRefresh' }));

// ---- the provider sheet's controls ----------------------------------------
function providerForm() {
  return {
    baseUrl: document.getElementById('p-base').value.trim(),
    model: document.getElementById('p-model').value.trim(),
    apiKey: document.getElementById('p-key').value,
  };
}
function setProviderStatus(text, cls) {
  providerStatus.textContent = text;
  providerStatus.className = 'form-status' + (cls ? ' ' + cls : '');
}
document.getElementById('p-save').addEventListener('click', () => {
  const form = providerForm();
  if (!form.baseUrl || !form.model) {
    setProviderStatus('Base URL and model are both required.', 'err');
    return;
  }
  setProviderStatus('Saving…');
  vscode.postMessage({ type: 'providerSave', ...form });
});
document.getElementById('p-test').addEventListener('click', () => {
  const form = providerForm();
  if (!form.baseUrl || !form.model) {
    if (providerHasKey) {
      setProviderStatus('Testing the saved provider…');
      vscode.postMessage({ type: 'providerTestSaved' });
      return;
    }
    setProviderStatus('Base URL and model are both required.', 'err');
    return;
  }
  setProviderStatus('Testing…');
  vscode.postMessage({ type: 'providerTest', ...form });
});
document.getElementById('p-fetch').addEventListener('click', () => {
  const form = providerForm();
  if (!form.baseUrl) {
    setProviderStatus('A base URL is required to fetch models.', 'err');
    return;
  }
  setProviderStatus('Fetching models…');
  vscode.postMessage({ type: 'providerModels', baseUrl: form.baseUrl, apiKey: form.apiKey });
});

// ---- messages --------------------------------------------------------------
window.addEventListener('message', (event) => {
  const msg = event.data;
  if (msg.type === 'sharedSent') {
    if ((drafts.get(msg.threadId) || '').trim() === msg.query) drafts.delete(msg.threadId);
    if (sharedThreadId === msg.threadId && q.value.trim() === msg.query) {
      q.value = '';
      q.style.height = 'auto';
    }
  } else if (msg.type === 'sharedState') {
    const nextId = msg.threadId || null;
    if (nextId !== sharedThreadId) {
      drafts.set(sharedThreadId || '', q.value);
      q.value = drafts.get(nextId || '') || '';
      q.style.height = 'auto';
      sharedThreadId = nextId;
      editor = null;
      if (nextId) setMode('ask');
      document.getElementById('topic-form').hidden = true;
      closeSheets();
    }
    const nextTopics = JSON.stringify((msg.threads || []).map(t => [t.id, t.title, t.path, t.updatedAt]));
    if (nextTopics !== topicSignature) {
      topicSignature = nextTopics;
      threads = (msg.threads || []).map(t => ({
        id: t.id, title: t.title, path: t.path, updatedAt: t.updatedAt,
      }));
      renderTopics();
    }
    sharedBusy = !!msg.busy;
    sendBtn.disabled = !!sharedThreadId && sharedBusy;
    document.getElementById('topic-new').disabled = sharedBusy;
    document.getElementById('topic-new-in-sheet').disabled = sharedBusy;
    topicStatus.textContent = msg.problem || (sharedThreadId ? 'Shared through Cinder · library Q&A via the Cinder provider' : 'Shared topics · choose a topic or create one');
    topicStatus.className = 'topic-status' + (msg.problem ? ' error' : '');
    // A fault in the shared channel must be visible without opening the sheet.
    historyBtn.classList.toggle('dot', !!msg.problem);
    const signature = JSON.stringify([sharedThreadId, msg.turns]);
    if (signature !== sharedSignature) {
      sharedSignature = signature;
      turns = msg.turns || [];
      renderFeed();
    }
  } else if (msg.type === 'init') {
    mode = msg.mode || 'ask';
    scope = msg.scope || scope;
    turns = msg.turns || [];
    setMode(mode);
    renderScope();
    renderFeed();
  } else if (msg.type === 'turn') {
    if (sharedThreadId) return;
    const turn = msg.turn;
    const at = turns.findIndex((t) => t.id === turn.id);
    if (at >= 0) turns[at] = turn;
    else turns.push(turn);
    renderFeed();
  } else if (msg.type === 'announce') {
    announcement = { level: msg.level || 'info', message: msg.message || '', action: msg.action };
    renderAnnounce();
  } else if (msg.type === 'scope') {
    scope = msg.scope || scope;
    renderScope();
  } else if (msg.type === 'mode') {
    setMode(msg.mode);
  } else if (msg.type === 'editor') {
    renderEditor(msg.turnId, msg.cardType, msg.cardId, msg.draft, msg.projects || [], !!msg.proposal, msg.warning);
  } else if (msg.type === 'editorError') {
    showEditorError(msg.message);
  } else if (msg.type === 'saved') {
    editor = null;
    renderFeed();
  } else if (msg.type === 'created') {
    // The new note joined the turn as a card of its own: open it, so the
    // reader sees what was just written.
    editor = null;
    expandedCards.add(msg.turnId + '/' + msg.cardType + '/' + msg.cardId);
    renderFeed();
  } else if (msg.type === 'projectCandidates') {
    candidates = msg.candidates || [];
    renderScope();
    if (!popEl.hidden) renderPop();
  } else if (msg.type === 'providerState') {
    document.getElementById('p-base').value = msg.baseUrl || '';
    document.getElementById('p-model').value = msg.model || '';
    const keyEl = document.getElementById('p-key');
    keyEl.value = '';
    providerHasKey = !!msg.hasKey;
    keyEl.placeholder = msg.hasKey
      ? 'Saved on this machine — leave blank to keep it'
      : 'Optional for local endpoints';
    document.getElementById('p-saved').textContent = msg.hasKey
      ? 'A key is saved for this endpoint. It is never shown again.'
      : 'No key saved — local endpoints often need none.';
    if (msg.error) setProviderStatus(msg.error, 'err');
    else if (!msg.baseUrl && !msg.model) {
      setProviderStatus('No provider configured yet — answers fail until one is saved.', 'err');
    } else setProviderStatus('Configured. Answers use this endpoint.', 'ok');
  } else if (msg.type === 'providerResult') {
    const list = document.getElementById('p-model-list');
    if (msg.kind === 'models' && msg.ok) {
      list.replaceChildren();
      for (const m of msg.models || []) {
        const opt = document.createElement('option');
        opt.value = m;
        list.appendChild(opt);
      }
      const modelEl = document.getElementById('p-model');
      if (!modelEl.value && (msg.models || []).length) modelEl.value = msg.models[0];
      if (msg.resolvedBaseUrl) document.getElementById('p-base').value = msg.resolvedBaseUrl;
    }
    if (msg.resolvedBaseUrl && msg.kind === 'test') {
      document.getElementById('p-base').value = msg.resolvedBaseUrl;
    }
    setProviderStatus(msg.message, msg.ok ? 'ok' : 'err');
    if (msg.kind === 'save' && msg.ok) {
      providerHasKey = providerHasKey || !!document.getElementById('p-key').value;
      document.getElementById('p-key').value = '';
      if (providerHasKey) {
        document.getElementById('p-key').placeholder = 'Saved on this machine — leave blank to keep it';
        document.getElementById('p-saved').textContent = 'A key is saved for this endpoint. It is never shown again.';
      }
    }
  }
});

setMode('ask');
renderScope();
renderTopics();
renderFeed();
// The host answers with the init message — a push before this script ran
// would be dropped, and the panel would come up empty.
vscode.postMessage({ type: 'ready' });
</script>
</body>
</html>`;
}
