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
// All four draw with `currentColor`, so they follow the theme by themselves.



const ICON_DESKTOP = `<svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><rect x="1.8" y="2.8" width="12.4" height="8.4" rx="1.2" fill="none" stroke="currentColor" stroke-width="1.3"/><path d="M5.5 13.5h5" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/></svg>`;

const KINDS = ["knowledge", "log", "idea", "other"];

/**
 * The brand mark, inlined so the webview needs no resource roots and no
 * image loads. Same construction as build/icon.svg (the brand SSOT):
 * ADR-0052 "The Knot" — one equal-width band threading itself, three
 * filled paths on a solid ink field, no frame. Generated paths must stay
 * byte-identical to the master.
 */
const MARK_SVG = `<svg viewBox="0 0 512 512" width="28" height="28" aria-hidden="true" focusable="false">
  <rect width="512" height="512" fill="#34322D"/>
  <g transform="translate(0 0)">
    <path fill="#F7F4EE" fill-rule="nonzero" d="M256.17 141.17L253.89 141.66L251.62 142.18L249.35 142.71L247.09 143.27L244.83 143.84L242.58 144.43L240.33 145.03L238.09 145.66L235.85 146.30L233.62 146.97L231.40 147.65L229.18 148.35L226.97 149.06L224.76 149.80L222.56 150.55L220.37 151.32L218.18 152.10L216.00 152.91L213.83 153.73L211.66 154.57L209.50 155.42L207.35 156.30L205.20 157.18L203.06 158.09L200.93 159.02L198.81 159.96L196.69 160.91L194.58 161.88L192.48 162.87L190.39 163.88L188.31 164.90L186.23 165.94L184.16 166.99L182.10 168.06L180.05 169.15L178.01 170.25L175.97 171.37L173.95 172.50L171.93 173.65L169.93 174.82L167.93 176.00L165.94 177.20L163.96 178.41L161.99 179.63L160.03 180.88L158.08 182.14L156.14 183.41L154.20 184.70L152.28 186.00L150.37 187.32L148.47 188.65L146.58 190.00L144.70 191.36L142.83 192.74L140.98 194.14L139.13 195.54L137.29 196.97L135.47 198.40L133.65 199.86L131.85 201.32L130.06 202.80L128.28 204.30L126.51 205.81L124.76 207.33L123.01 208.87L121.28 210.42L119.56 211.99L117.86 213.57L116.16 215.16L114.48 216.77L112.81 218.40L111.16 220.03L109.52 221.68L107.89 223.35L106.27 225.02L104.67 226.72L103.08 228.42L101.51 230.14L99.95 231.87L98.40 233.62L96.87 235.38L95.36 237.16L93.86 238.94L92.37 240.74L90.90 242.56L89.44 244.38L88.01 246.22L86.58 248.08L85.17 249.94L83.78 251.82L82.41 253.71L81.05 255.62L79.70 257.54L78.38 259.47L77.07 261.42L75.78 263.37L74.51 265.34L73.25 267.32L72.02 269.32L70.80 271.32L69.60 273.34L68.42 275.38L67.26 277.42L66.12 279.48L64.99 281.55L63.89 283.63L62.81 285.72L61.75 287.82L60.71 289.94L59.69 292.07L58.69 294.21L57.71 296.36L56.76 298.52L55.82 300.70L54.91 302.88L54.03 305.08L53.16 307.29L52.32 309.51L51.51 311.74L50.72 313.98L49.95 316.23L49.21 318.49L48.49 320.76L47.80 323.04L47.14 325.33L46.50 327.63L45.90 329.95L45.31 332.27L44.76 334.60L44.24 336.93L43.74 339.28L43.28 341.64L42.84 344.00L42.44 346.38L42.06 348.76L41.72 351.14L41.41 353.54L41.13 355.94L40.89 358.35L40.68 360.77L40.51 363.19L40.37 365.62L40.27 368.05L40.20 370.48L40.17 372.92L40.18 375.37L40.22 377.82L40.31 380.27L40.44 382.72L40.60 385.18L40.81 387.63L41.06 390.09L41.36 392.54L41.69 395.00L42.08 397.45L42.50 399.90L42.97 402.35L43.49 404.79L44.06 407.22L44.67 409.65L45.34 412.07L46.05 414.48L46.81 416.89L47.63 419.28L48.49 421.66L49.41 424.02L50.38 426.37L51.41 428.70L52.49 431.02L53.62 433.31L54.81 435.58L56.05 437.83L57.35 440.05L58.71 442.25L60.12 444.41L61.58 446.55L63.10 448.65L64.68 450.71L66.31 452.74L68.00 454.73L69.74 456.68L71.53 458.58L73.38 460.44L75.28 462.25L77.23 464.01L79.22 465.72L81.27 467.38L83.36 468.97L85.50 470.52L87.67 472.00L89.90 473.42L92.15 474.78L94.45 476.08L96.78 477.31L99.15 478.48L101.54 479.58L103.96 480.61L106.41 481.58L108.88 482.48L111.37 483.31L113.89 484.07L116.41 484.76L118.95 485.38L121.51 485.94L124.07 486.43L126.64 486.85L129.22 487.20L131.80 487.49L134.38 487.71L136.96 487.87L139.54 487.96L142.12 488.00L144.69 487.97L147.25 487.88L149.80 487.73L152.35 487.53L154.89 487.27L157.41 486.95L159.92 486.58L162.42 486.16L164.90 485.69L167.37 485.17L169.82 484.59L172.26 483.97L174.67 483.30L177.07 482.59L179.45 481.83L181.81 481.03L184.15 480.19L186.47 479.31L188.77 478.38L191.05 477.42L193.31 476.42L195.55 475.39L197.77 474.31L199.96 473.21L202.13 472.07L204.29 470.90L206.41 469.69L208.52 468.46L210.60 467.19L212.67 465.89L214.70 464.57L216.72 463.21L218.72 461.83L220.69 460.43L222.64 458.99L224.56 457.54L226.47 456.05L228.35 454.55L230.21 453.02L232.05 451.47L233.86 449.89L235.66 448.30L237.43 446.68L239.17 445.04L240.90 443.39L242.61 441.71L244.29 440.01L245.95 438.30L247.59 436.57L249.20 434.82L250.80 433.05L252.37 431.27L253.92 429.47L255.45 427.65L256.96 425.82L258.45 423.97L259.91 422.11L261.36 420.23L262.78 418.34L264.18 416.44L265.56 414.52L266.93 412.59L268.27 410.65L269.58 408.69L270.88 406.72L272.16 404.74L273.42 402.75L274.65 400.75L275.87 398.73L277.07 396.71L278.24 394.68L279.40 392.63L280.53 390.57L281.65 388.51L282.74 386.43L283.82 384.35L284.88 382.26L285.91 380.16L286.93 378.05L287.92 375.93L254.92 371.43L253.97 373.24L253.00 375.05L252.01 376.84L251.01 378.62L249.99 380.40L248.95 382.16L247.90 383.91L246.83 385.65L245.74 387.38L244.64 389.09L243.52 390.80L242.39 392.49L241.24 394.17L240.07 395.83L238.88 397.48L237.68 399.12L236.46 400.74L235.23 402.35L233.98 403.95L232.72 405.53L231.43 407.09L230.14 408.64L228.82 410.18L227.49 411.69L226.14 413.19L224.78 414.67L223.40 416.14L222.01 417.59L220.60 419.01L219.17 420.42L217.73 421.82L216.27 423.19L214.80 424.54L213.31 425.87L211.81 427.18L210.29 428.47L208.76 429.74L207.21 430.98L205.65 432.20L204.08 433.40L202.49 434.58L200.88 435.73L199.27 436.86L197.64 437.96L195.99 439.03L194.34 440.08L192.67 441.10L190.98 442.10L189.29 443.07L187.59 444.00L185.87 444.91L184.14 445.79L182.40 446.64L180.65 447.46L178.89 448.24L177.12 448.99L175.35 449.71L173.56 450.40L171.77 451.06L169.97 451.68L168.16 452.26L166.35 452.80L164.53 453.31L162.70 453.79L160.88 454.22L159.05 454.62L157.21 454.98L155.37 455.30L153.54 455.58L151.70 455.82L149.86 456.02L148.03 456.17L146.19 456.29L144.36 456.36L142.54 456.39L140.72 456.37L138.90 456.31L137.10 456.21L135.30 456.07L133.51 455.88L131.73 455.64L129.96 455.36L128.20 455.04L126.45 454.67L124.72 454.26L123.01 453.80L121.31 453.29L119.63 452.75L117.96 452.16L116.32 451.51L114.69 450.83L113.09 450.11L111.51 449.34L109.95 448.52L108.41 447.67L106.90 446.77L105.41 445.83L103.95 444.85L102.52 443.83L101.11 442.77L99.73 441.67L98.39 440.53L97.07 439.35L95.78 438.13L94.52 436.88L93.29 435.59L92.10 434.27L90.93 432.92L89.80 431.53L88.71 430.10L87.64 428.65L86.61 427.17L85.62 425.66L84.66 424.12L83.73 422.55L82.84 420.96L81.98 419.34L81.16 417.70L80.37 416.04L79.62 414.35L78.91 412.64L78.23 410.91L77.58 409.16L76.97 407.40L76.39 405.62L75.85 403.82L75.35 402.00L74.88 400.17L74.44 398.33L74.04 396.47L73.67 394.61L73.33 392.73L73.03 390.84L72.76 388.94L72.53 387.03L72.33 385.12L72.16 383.20L72.02 381.27L71.91 379.33L71.83 377.39L71.79 375.45L71.78 373.50L71.79 371.54L71.84 369.59L71.92 367.63L72.02 365.67L72.16 363.70L72.32 361.74L72.52 359.77L72.74 357.81L72.98 355.85L73.26 353.88L73.56 351.92L73.89 349.96L74.24 348.00L74.62 346.05L75.03 344.09L75.46 342.14L75.92 340.19L76.40 338.25L76.90 336.31L77.43 334.37L77.99 332.44L78.56 330.51L79.16 328.59L79.78 326.67L80.43 324.76L81.10 322.86L81.79 320.96L82.50 319.06L83.23 317.18L83.99 315.30L84.76 313.42L85.56 311.56L86.37 309.70L87.21 307.84L88.06 306.00L88.94 304.16L89.83 302.33L90.75 300.51L91.68 298.70L92.63 296.89L93.60 295.10L94.59 293.31L95.60 291.53L96.62 289.76L97.66 287.99L98.72 286.24L99.80 284.50L100.89 282.77L102.00 281.04L103.12 279.33L104.26 277.62L105.42 275.92L106.60 274.24L107.79 272.56L108.99 270.90L110.21 269.24L111.45 267.60L112.70 265.96L113.96 264.34L115.24 262.73L116.54 261.12L117.85 259.53L119.17 257.95L120.51 256.38L121.86 254.82L123.22 253.27L124.60 251.74L125.99 250.21L127.39 248.69L128.81 247.19L130.24 245.70L131.68 244.22L133.14 242.75L134.61 241.30L136.09 239.85L137.58 238.42L139.08 237.00L140.60 235.59L142.13 234.19L143.66 232.81L145.21 231.43L146.77 230.07L148.35 228.73L149.93 227.39L151.52 226.07L153.13 224.76L154.74 223.46L156.37 222.18L158.01 220.90L159.65 219.64L161.31 218.39L162.98 217.16L164.65 215.94L166.34 214.74L168.03 213.54L169.74 212.36L171.45 211.19L173.18 210.04L174.91 208.90L176.65 207.77L178.40 206.65L180.16 205.56L181.93 204.47L183.70 203.40L185.49 202.34L187.28 201.29L189.08 200.26L190.89 199.25L192.70 198.24L194.53 197.25L196.36 196.28L198.20 195.32L200.04 194.38L201.90 193.45L203.76 192.53L205.62 191.62L207.50 190.74L209.38 189.87L211.27 189.01L213.16 188.17L215.06 187.34L216.97 186.53L218.89 185.73L220.80 184.95L222.73 184.18L224.66 183.43L226.60 182.69L228.54 181.97L230.49 181.27L232.44 180.58L234.40 179.90L236.36 179.25L238.33 178.61L240.30 177.98L242.28 177.37L244.26 176.78L246.25 176.20L248.24 175.64L250.24 175.10L252.23 174.57L254.24 174.06L256.24 173.56L258.25 173.09L260.27 172.63L262.28 172.19L264.30 171.76L266.32 171.35L268.35 170.96Z"/>
    <path fill="#F7F4EE" fill-rule="nonzero" d="M299.80 345.64L300.51 343.42L301.20 341.20L301.87 338.97L302.52 336.73L303.16 334.49L303.77 332.24L304.37 329.99L304.95 327.74L305.51 325.48L306.05 323.22L306.57 320.95L307.08 318.68L307.57 316.41L308.03 314.13L308.48 311.85L308.91 309.56L309.32 307.27L309.72 304.98L310.09 302.69L310.45 300.40L310.79 298.10L311.11 295.80L311.41 293.49L311.70 291.19L311.96 288.88L312.21 286.57L312.44 284.26L312.65 281.95L312.85 279.64L313.02 277.32L313.18 275.00L313.32 272.69L313.44 270.37L313.54 268.05L313.62 265.73L313.69 263.41L313.74 261.09L313.77 258.77L313.78 256.45L313.78 254.13L313.75 251.81L313.71 249.49L313.66 247.17L313.58 244.85L313.48 242.53L313.36 240.21L313.23 237.89L313.08 235.57L312.91 233.26L312.73 230.95L312.52 228.63L312.30 226.32L312.06 224.01L311.80 221.70L311.52 219.40L311.23 217.09L310.91 214.79L310.58 212.49L310.23 210.20L309.86 207.90L309.48 205.61L309.07 203.32L308.65 201.04L308.21 198.75L307.75 196.48L307.27 194.20L306.77 191.93L306.26 189.66L305.72 187.39L305.17 185.13L304.60 182.88L304.01 180.63L303.40 178.38L302.77 176.14L302.13 173.90L301.46 171.67L300.78 169.44L300.07 167.22L299.36 165.00L298.62 162.79L297.85 160.58L297.08 158.38L296.28 156.19L295.46 154.00L294.63 151.82L293.78 149.65L292.90 147.48L292.01 145.32L291.09 143.17L290.16 141.02L289.21 138.89L288.24 136.75L287.25 134.63L286.24 132.52L285.21 130.42L284.16 128.32L283.09 126.23L282.00 124.16L280.90 122.09L279.77 120.03L278.61 117.98L277.44 115.94L276.26 113.91L275.05 111.90L273.82 109.89L272.57 107.89L271.30 105.91L270.00 103.94L268.69 101.98L267.36 100.03L266.00 98.10L264.63 96.18L263.23 94.27L261.82 92.37L260.38 90.49L258.92 88.63L257.44 86.77L255.94 84.94L254.42 83.12L252.87 81.31L251.30 79.52L249.72 77.75L248.11 75.99L246.47 74.26L244.82 72.54L243.15 70.83L241.45 69.15L239.73 67.49L237.99 65.84L236.23 64.22L234.44 62.62L232.63 61.04L230.80 59.48L228.95 57.94L227.08 56.43L225.18 54.94L223.26 53.47L221.32 52.03L219.35 50.62L217.36 49.23L215.35 47.86L213.32 46.53L211.27 45.23L209.19 43.95L207.09 42.70L204.97 41.49L202.83 40.30L200.66 39.15L198.47 38.03L196.27 36.95L194.03 35.91L191.78 34.90L189.51 33.92L187.21 32.99L184.90 32.09L182.56 31.24L180.21 30.42L177.84 29.65L175.44 28.92L173.03 28.24L170.60 27.61L168.16 27.02L165.70 26.47L163.22 25.98L160.73 25.55L158.22 25.16L155.70 24.83L153.17 24.55L150.62 24.32L148.07 24.16L145.51 24.05L142.94 24.00L140.37 24.02L137.79 24.09L135.21 24.23L132.63 24.43L130.05 24.70L127.47 25.03L124.90 25.43L122.33 25.90L119.77 26.43L117.23 27.03L114.69 27.70L112.18 28.44L109.68 29.25L107.20 30.12L104.75 31.07L102.31 32.08L99.91 33.16L97.54 34.30L95.20 35.52L92.89 36.79L90.62 38.13L88.38 39.54L86.19 41.00L84.04 42.52L81.93 44.11L79.87 45.74L77.86 47.43L75.90 49.18L73.98 50.97L72.12 52.81L70.31 54.70L68.55 56.64L66.85 58.61L65.20 60.63L63.60 62.68L62.06 64.77L60.58 66.90L59.15 69.05L57.78 71.24L56.46 73.45L55.20 75.69L54.00 77.96L52.85 80.25L51.75 82.55L50.70 84.88L49.72 87.22L48.78 89.58L47.90 91.96L47.07 94.34L46.29 96.74L45.56 99.15L44.88 101.57L44.25 104.00L43.67 106.43L43.14 108.87L42.65 111.31L42.20 113.76L41.81 116.21L41.46 118.67L41.15 121.12L40.89 123.58L40.67 126.04L40.49 128.49L40.35 130.94L40.25 133.40L40.19 135.84L40.17 138.29L40.18 140.73L40.24 143.17L40.33 145.60L40.46 148.03L40.62 150.46L40.82 152.87L41.05 155.28L41.32 157.69L41.62 160.09L41.95 162.48L42.31 164.86L42.71 167.24L43.13 169.60L43.59 171.96L44.07 174.31L44.59 176.65L45.13 178.99L45.70 181.31L46.30 183.62L46.93 185.93L47.59 188.22L48.27 190.51L48.97 192.78L49.71 195.05L50.47 197.30L51.25 199.55L52.06 201.78L52.89 204.00L53.75 206.21L54.63 208.41L55.53 210.60L56.45 212.78L57.40 214.95L58.37 217.10L59.36 219.25L60.37 221.38L61.41 223.50L62.47 225.61L63.54 227.70L64.64 229.79L65.75 231.86L66.89 233.92L68.04 235.97L69.22 238.01L70.41 240.03L71.63 242.04L72.86 244.04L74.11 246.02L75.37 248.00L76.66 249.96L77.96 251.91L79.28 253.84L80.61 255.76L101.01 229.43L99.92 227.70L98.84 225.96L97.78 224.20L96.74 222.44L95.71 220.67L94.70 218.89L93.71 217.11L92.74 215.31L91.79 213.51L90.85 211.70L89.94 209.88L89.04 208.05L88.16 206.21L87.31 204.37L86.47 202.51L85.65 200.65L84.85 198.79L84.07 196.92L83.32 195.04L82.58 193.15L81.87 191.26L81.17 189.36L80.50 187.45L79.86 185.54L79.23 183.63L78.63 181.70L78.05 179.78L77.50 177.85L76.96 175.91L76.46 173.97L75.97 172.03L75.51 170.08L75.08 168.13L74.67 166.18L74.28 164.22L73.93 162.26L73.60 160.30L73.29 158.34L73.01 156.38L72.76 154.41L72.54 152.45L72.34 150.48L72.18 148.52L72.04 146.56L71.93 144.60L71.85 142.64L71.80 140.68L71.78 138.73L71.79 136.78L71.83 134.83L71.90 132.89L72.00 130.95L72.14 129.02L72.31 127.10L72.50 125.18L72.74 123.27L73.00 121.38L73.30 119.49L73.63 117.61L73.99 115.74L74.39 113.88L74.83 112.04L75.29 110.21L75.79 108.39L76.33 106.59L76.90 104.80L77.51 103.04L78.15 101.29L78.83 99.56L79.54 97.84L80.28 96.15L81.07 94.49L81.89 92.84L82.74 91.22L83.62 89.63L84.55 88.06L85.51 86.51L86.50 85.00L87.52 83.51L88.58 82.06L89.68 80.63L90.80 79.24L91.96 77.88L93.16 76.55L94.38 75.26L95.63 74.00L96.92 72.79L98.24 71.61L99.58 70.46L100.96 69.35L102.36 68.29L103.79 67.26L105.25 66.28L106.73 65.33L108.24 64.43L109.77 63.57L111.33 62.75L112.91 61.98L114.51 61.25L116.13 60.56L117.78 59.92L119.44 59.32L121.12 58.77L122.81 58.26L124.53 57.79L126.26 57.37L128.00 57.00L129.75 56.67L131.52 56.39L133.30 56.15L135.09 55.95L136.89 55.80L138.70 55.70L140.51 55.63L142.33 55.61L144.16 55.64L145.99 55.70L147.82 55.81L149.66 55.96L151.49 56.16L153.33 56.39L155.17 56.67L157.00 56.98L158.84 57.33L160.67 57.73L162.50 58.16L164.32 58.63L166.14 59.14L167.96 59.68L169.76 60.26L171.57 60.87L173.36 61.52L175.15 62.21L176.92 62.92L178.69 63.67L180.45 64.45L182.20 65.27L183.94 66.11L185.67 66.99L187.39 67.89L189.10 68.83L190.79 69.79L192.48 70.78L194.15 71.80L195.81 72.85L197.45 73.92L199.08 75.02L200.70 76.14L202.31 77.29L203.90 78.46L205.48 79.66L207.04 80.88L208.59 82.12L210.12 83.39L211.64 84.67L213.14 85.98L214.63 87.31L216.11 88.66L217.56 90.03L219.01 91.42L220.43 92.82L221.85 94.25L223.24 95.70L224.62 97.16L225.99 98.64L227.34 100.14L228.67 101.65L229.99 103.18L231.29 104.73L232.57 106.29L233.84 107.87L235.09 109.46L236.32 111.07L237.54 112.69L238.75 114.33L239.93 115.98L241.10 117.64L242.26 119.32L243.40 121.01L244.51 122.71L245.62 124.43L246.71 126.15L247.78 127.89L248.83 129.64L249.87 131.40L250.89 133.17L251.90 134.96L252.89 136.75L253.86 138.55L254.81 140.37L255.75 142.19L256.67 144.02L257.58 145.86L258.47 147.71L259.34 149.57L260.20 151.44L261.04 153.32L261.86 155.20L262.67 157.09L263.46 158.99L264.23 160.90L264.99 162.82L265.73 164.74L266.45 166.67L267.16 168.61L267.86 170.55L268.53 172.50L269.19 174.45L269.83 176.42L270.45 178.38L271.07 180.36L271.66 182.33L272.24 184.32L272.79 186.31L273.34 188.30L273.87 190.30L274.38 192.31L274.87 194.31L275.35 196.33L275.81 198.34L276.26 200.37L276.69 202.39L277.11 204.42L277.50 206.45L277.88 208.49L278.25 210.53L278.60 212.57L278.93 214.61L279.24 216.66L279.54 218.71L279.83 220.76L280.10 222.82L280.35 224.88L280.58 226.94L280.80 229.00L281.00 231.06L281.19 233.13L281.36 235.19L281.51 237.26L281.65 239.33L281.77 241.40L281.88 243.48L281.97 245.55L282.04 247.62L282.10 249.69L282.14 251.77L282.17 253.84L282.18 255.92L282.17 257.99L282.14 260.07L282.10 262.14L282.05 264.21L281.98 266.29L281.89 268.36L281.78 270.43L281.66 272.50L281.53 274.57L281.37 276.64L281.20 278.71L281.02 280.77L280.82 282.84L280.60 284.90L280.37 286.96L280.12 289.02L279.85 291.07L279.57 293.13L279.27 295.18L278.96 297.22L278.62 299.27L278.27 301.31L277.91 303.35L277.53 305.39L277.14 307.42L276.72 309.45L276.29 311.47L275.85 313.50L275.39 315.51L274.91 317.53L274.42 319.53L273.91 321.54L273.38 323.54L272.84 325.53L272.28 327.52L271.71 329.51L271.11 331.49L270.50 333.46L269.88 335.43L269.24 337.39L268.58 339.35L267.91 341.30Z"/>
    <path fill="#F7F4EE" fill-rule="nonzero" d="M100.90 281.19L102.47 282.91L104.05 284.63L105.65 286.32L107.26 288.01L108.88 289.67L110.52 291.33L112.17 292.97L113.83 294.60L115.51 296.22L117.20 297.82L118.90 299.40L120.62 300.97L122.34 302.53L124.08 304.08L125.83 305.60L127.60 307.12L129.37 308.62L131.16 310.11L132.95 311.58L134.76 313.04L136.59 314.48L138.42 315.91L140.26 317.32L142.11 318.72L143.98 320.10L145.86 321.47L147.74 322.83L149.64 324.17L151.54 325.49L153.46 326.80L155.39 328.09L157.33 329.37L159.27 330.64L161.23 331.89L163.20 333.12L165.17 334.34L167.16 335.54L169.15 336.73L171.16 337.90L173.17 339.05L175.19 340.19L177.22 341.32L179.26 342.43L181.31 343.52L183.36 344.59L185.43 345.65L187.50 346.70L189.58 347.73L191.67 348.74L193.77 349.73L195.88 350.71L197.99 351.68L200.11 352.62L202.24 353.55L204.37 354.46L206.52 355.36L208.67 356.24L210.82 357.10L212.99 357.95L215.16 358.77L217.34 359.59L219.52 360.38L221.71 361.16L223.91 361.92L226.11 362.65L228.32 363.38L230.54 364.08L232.76 364.77L234.99 365.44L237.22 366.09L239.46 366.73L241.71 367.34L243.96 367.94L246.21 368.52L248.48 369.08L250.74 369.62L253.01 370.14L255.29 370.64L257.57 371.13L259.85 371.59L262.14 372.04L264.44 372.46L266.74 372.87L269.04 373.26L271.34 373.62L273.65 373.97L275.97 374.30L278.28 374.60L280.60 374.89L282.93 375.15L285.25 375.40L287.58 375.63L289.92 375.83L292.25 376.01L294.59 376.17L296.93 376.31L299.27 376.42L301.61 376.52L303.96 376.60L306.31 376.65L308.66 376.67L311.01 376.68L313.36 376.66L315.71 376.63L318.06 376.56L320.42 376.48L322.77 376.37L325.12 376.24L327.48 376.08L329.83 375.90L332.18 375.69L334.53 375.46L336.88 375.21L339.23 374.93L341.58 374.63L343.93 374.30L346.27 373.94L348.61 373.56L350.95 373.15L353.29 372.71L355.62 372.25L357.95 371.76L360.27 371.25L362.59 370.70L364.91 370.13L367.22 369.53L369.53 368.90L371.83 368.25L374.12 367.56L376.41 366.85L378.69 366.10L380.96 365.33L383.23 364.52L385.49 363.68L387.73 362.82L389.97 361.92L392.20 360.99L394.42 360.03L396.63 359.03L398.83 358.01L401.01 356.95L403.18 355.85L405.34 354.73L407.48 353.57L409.61 352.37L411.72 351.14L413.82 349.88L415.90 348.58L417.96 347.24L420.00 345.87L422.03 344.46L424.03 343.02L426.01 341.53L427.96 340.02L429.90 338.46L431.81 336.87L433.69 335.23L435.54 333.57L437.37 331.86L439.16 330.11L440.93 328.32L442.66 326.50L444.36 324.64L446.03 322.74L447.65 320.80L449.24 318.82L450.79 316.80L452.30 314.75L453.76 312.66L455.18 310.53L456.55 308.37L457.88 306.17L459.16 303.93L460.38 301.66L461.55 299.35L462.67 297.02L463.72 294.65L464.72 292.25L465.66 289.82L466.55 287.37L467.36 284.89L468.11 282.38L468.80 279.85L469.42 277.31L469.97 274.74L470.45 272.15L470.86 269.56L471.20 266.94L471.47 264.32L471.66 261.69L471.78 259.06L471.83 256.42L471.81 253.79L471.71 251.15L471.54 248.52L471.29 245.90L470.98 243.28L470.59 240.68L470.13 238.09L469.60 235.52L469.00 232.96L468.34 230.43L467.61 227.91L466.81 225.43L465.96 222.96L465.03 220.52L464.05 218.12L463.01 215.74L461.92 213.39L460.76 211.08L459.55 208.80L458.29 206.55L456.99 204.34L455.63 202.16L454.22 200.02L452.77 197.92L451.28 195.85L449.74 193.82L448.17 191.83L446.55 189.88L444.90 187.97L443.21 186.09L441.49 184.26L439.73 182.46L437.95 180.70L436.13 178.98L434.29 177.30L432.41 175.65L430.51 174.05L428.59 172.48L426.64 170.95L424.67 169.46L422.67 168.00L420.65 166.58L418.62 165.19L416.56 163.85L414.49 162.53L412.40 161.26L410.29 160.02L408.17 158.81L406.03 157.64L403.88 156.50L401.71 155.40L399.53 154.33L397.34 153.29L395.13 152.29L392.92 151.32L390.69 150.38L388.45 149.47L386.21 148.59L383.95 147.74L381.69 146.93L379.42 146.15L377.14 145.39L374.86 144.67L372.57 143.97L370.27 143.30L367.96 142.67L365.65 142.06L363.34 141.48L361.02 140.92L358.70 140.40L356.37 139.90L354.04 139.43L351.70 138.99L349.36 138.57L347.02 138.18L344.68 137.82L342.33 137.48L339.99 137.17L337.64 136.88L335.29 136.61L332.94 136.38L330.59 136.16L328.23 135.97L325.88 135.81L323.53 135.67L321.17 135.56L318.82 135.46L316.47 135.39L314.11 135.35L311.76 135.32L309.41 135.32L307.06 135.34L304.71 135.39L302.37 135.45L300.02 135.54L297.68 135.65L295.34 135.78L293.00 135.94L290.67 136.11L288.33 136.31L300.94 167.14L302.99 167.06L305.04 167.00L307.08 166.95L309.13 166.93L311.17 166.93L313.22 166.95L315.26 166.98L317.30 167.04L319.34 167.11L321.38 167.21L323.41 167.33L325.45 167.46L327.47 167.62L329.50 167.80L331.52 168.00L333.54 168.22L335.56 168.47L337.57 168.73L339.57 169.01L341.58 169.32L343.57 169.65L345.56 170.00L347.55 170.37L349.53 170.76L351.50 171.18L353.46 171.62L355.42 172.08L357.37 172.57L359.31 173.07L361.25 173.60L363.17 174.16L365.09 174.73L367.00 175.33L368.89 175.95L370.78 176.60L372.65 177.27L374.52 177.96L376.37 178.68L378.21 179.42L380.03 180.19L381.85 180.97L383.64 181.79L385.43 182.62L387.20 183.49L388.95 184.37L390.69 185.28L392.41 186.22L394.11 187.18L395.79 188.16L397.46 189.17L399.11 190.20L400.73 191.26L402.33 192.34L403.92 193.45L405.47 194.58L407.01 195.73L408.52 196.91L410.01 198.11L411.48 199.34L412.91 200.59L414.32 201.86L415.70 203.16L417.05 204.48L418.37 205.82L419.67 207.19L420.93 208.57L422.15 209.98L423.35 211.41L424.51 212.87L425.63 214.34L426.73 215.83L427.78 217.34L428.79 218.87L429.77 220.42L430.71 221.99L431.61 223.57L432.46 225.17L433.28 226.79L434.05 228.42L434.78 230.06L435.47 231.72L436.11 233.40L436.71 235.08L437.26 236.78L437.77 238.48L438.23 240.19L438.64 241.92L439.01 243.65L439.33 245.39L439.60 247.13L439.82 248.88L440.00 250.63L440.12 252.39L440.19 254.14L440.22 255.90L440.20 257.66L440.13 259.42L440.01 261.17L439.84 262.92L439.62 264.67L439.36 266.42L439.05 268.15L438.69 269.89L438.28 271.61L437.83 273.33L437.32 275.03L436.78 276.73L436.19 278.41L435.54 280.09L434.86 281.75L434.14 283.40L433.37 285.03L432.56 286.65L431.70 288.25L430.81 289.84L429.88 291.40L428.90 292.95L427.89 294.49L426.85 296.00L425.76 297.50L424.64 298.97L423.48 300.42L422.29 301.85L421.07 303.27L419.81 304.66L418.52 306.02L417.20 307.37L415.85 308.69L414.48 309.99L413.07 311.27L411.64 312.52L410.18 313.75L408.69 314.95L407.18 316.14L405.65 317.29L404.09 318.43L402.51 319.54L400.91 320.62L399.29 321.68L397.65 322.72L395.98 323.73L394.30 324.71L392.60 325.68L390.88 326.61L389.15 327.53L387.40 328.42L385.63 329.28L383.85 330.12L382.05 330.94L380.24 331.73L378.41 332.49L376.58 333.24L374.73 333.96L372.86 334.65L370.99 335.33L369.11 335.97L367.21 336.60L365.31 337.20L363.39 337.78L361.47 338.34L359.53 338.87L357.59 339.38L355.64 339.86L353.69 340.33L351.72 340.77L349.75 341.19L347.77 341.59L345.79 341.96L343.80 342.31L341.80 342.64L339.80 342.95L337.79 343.24L335.78 343.50L333.77 343.75L331.75 343.97L329.73 344.18L327.70 344.36L325.68 344.52L323.64 344.66L321.61 344.78L319.57 344.88L317.53 344.95L315.49 345.01L313.45 345.05L311.41 345.07L309.36 345.07L307.31 345.05L305.27 345.01L303.22 344.95L301.17 344.87L299.12 344.77L297.08 344.65L295.03 344.52L292.98 344.36L290.93 344.19L288.89 343.99L286.84 343.78L284.80 343.55L282.76 343.31L280.72 343.04L278.68 342.76L276.64 342.46L274.60 342.13L272.57 341.80L270.54 341.44L268.51 341.07L266.49 340.68L264.46 340.27L262.44 339.85L260.43 339.41L258.41 338.95L256.40 338.47L254.40 337.98L252.39 337.47L250.39 336.94L248.40 336.40L246.41 335.84L244.42 335.27L242.44 334.68L240.46 334.07L238.49 333.44L236.52 332.80L234.55 332.15L232.60 331.48L230.64 330.79L228.69 330.08L226.75 329.36L224.81 328.63L222.88 327.88L220.96 327.11L219.04 326.33L217.12 325.54L215.21 324.73L213.31 323.90L211.42 323.06L209.53 322.20L207.65 321.33L205.77 320.45L203.91 319.54L202.04 318.63L200.19 317.70L198.34 316.76L196.50 315.80L194.67 314.82L192.85 313.83L191.03 312.83L189.22 311.82L187.42 310.79L185.63 309.74L183.84 308.69L182.07 307.62L180.30 306.53L178.54 305.43L176.79 304.32L175.05 303.19L173.31 302.05L171.59 300.90L169.87 299.73L168.17 298.55L166.47 297.36L164.79 296.15L163.11 294.93L161.44 293.70L159.78 292.46L158.14 291.20L156.50 289.93L154.87 288.64L153.26 287.35L151.65 286.04L150.06 284.71L148.47 283.38L146.90 282.03L145.34 280.68L143.79 279.30L142.25 277.92L140.72 276.52L139.20 275.12L137.70 273.70L136.20 272.26L134.72 270.82L133.26 269.36L131.80 267.90L130.35 266.42L128.92 264.93L127.51 263.42L126.10 261.91L124.71 260.39L123.33 258.85L121.96 257.30L120.61 255.75Z"/>
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
    margin: 0; display: flex; flex-direction: column; overflow: hidden;
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
     moved in here (ADR-0044 decision 1). */
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
    flex: none; width: 24px; height: 24px; cursor: pointer;
    display: flex; align-items: center; justify-content: center;
    background: none; border: none; border-radius: 4px;
    color: var(--vscode-foreground, #333);
  }
  .icon-btn:hover { background: var(--vscode-toolbar-hoverBackground, rgba(128,128,128,.18)); }
  .icon-btn:focus-visible { outline: 1px solid var(--r-accent); outline-offset: -1px; }
  .icon-btn svg { width: 16px; height: 16px; display: block; }
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
  .shared-topics { padding: 8px 12px; border-bottom: 1px solid var(--r-border); }
  .topic-row { display: flex; gap: 6px; align-items: center; min-width: 0; }
  .topic-row[hidden] { display: none; }
  .topic-row select, .topic-row input { flex: 1; min-width: 0; width: 0; padding: 6px 8px; border: 1px solid var(--r-border); border-radius: 8px; background: var(--vscode-input-background, transparent); color: var(--vscode-foreground, inherit); font: inherit; }
  .topic-row button { cursor: pointer; color: var(--vscode-foreground, inherit); background: transparent; border: 1px solid var(--r-border); border-radius: 6px; padding: 5px; }
  #topic-form { margin-top: 8px; }
  #topic-status { margin: 6px 0 0; font-size: 11px; line-height: 1.5; color: var(--r-muted); overflow-wrap: anywhere; }
  #topic-status.error { color: var(--vscode-errorForeground, #c33); }
  button:focus-visible, select:focus-visible, input:focus-visible, textarea:focus-visible { outline: 2px solid var(--vscode-focusBorder, currentColor); outline-offset: 2px; }
  /* ---------- the empty state ---------- */
  .empty { padding: 18px 12px; font-size: 12px; line-height: 1.6; color: var(--r-muted); }
  .empty h2 { margin: 0 0 8px; font-size: 14px; font-weight: 700; color: var(--vscode-foreground, #333); }
  .empty p { margin: 0 0 8px; }
  .empty .mark { width: 44px; height: 44px; margin-bottom: 12px; }
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
    <button class="icon-btn" id="act-desktop" type="button" title="Open the Cinder Desktop app" aria-label="Open the Cinder Desktop app">${ICON_DESKTOP}</button>
  </span>
</header>
<section class="shared-topics" aria-label="Shared conversation topics">
  <div class="topic-row">
    <select id="topic-select" aria-label="Conversation topic"><option value="">Local knowledge console</option></select>
    <button class="icon-btn" id="topic-new" type="button" aria-label="New shared conversation" title="New shared conversation">+</button>
    <button class="icon-btn" id="topic-refresh" type="button" aria-label="Refresh shared conversations" title="Refresh shared conversations">↻</button>
  </div>
  <form id="topic-form" class="topic-row" hidden>
    <input id="topic-title" aria-label="Conversation title" placeholder="Name this conversation" maxlength="160" required>
    <button type="submit">Create</button>
    <button id="topic-cancel" type="button">Cancel</button>
  </form>
  <p id="topic-status" role="status">Shared through Cinder · connect Cinder MCP to sync</p>
</section>
<div class="announce" id="announce" hidden>
  <span class="msg" id="announce-msg"></span>
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
<script nonce="${nonce}">
const MARK_SVG = ${JSON.stringify(MARK_SVG)};
const KINDS = ${JSON.stringify(KINDS)};
const VISIBLE_TURNS = ${VISIBLE_TURNS};
const vscode = acquireVsCodeApi();
const feedEl = document.getElementById('feed');
const announceEl = document.getElementById('announce');
const announceMsg = document.getElementById('announce-msg');
const q = document.getElementById('q');
const sendBtn = document.getElementById('send');
const chip = document.getElementById('chip');
const modeBtns = Array.from(document.querySelectorAll('.mode-btn'));
const popEl = document.getElementById('project-pop');

// ---- state -----------------------------------------------------------------
// The host owns the DATA (the turns); this side owns the FOLD STATE. A
// collapsed turn must stay collapsed while other turns arrive.
let mode = 'ask';
let scope = { projectId: 0, projectName: '', repository: '', locked: false };
let turns = [];
let sharedThreadId = null;
let sharedBusy = false;
let sharedSignature = '';
const drafts = new Map();
const topicSelect = document.getElementById('topic-select');
const topicStatus = document.getElementById('topic-status');
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
  if (!announcement) { announceEl.hidden = true; return; }
  announceEl.hidden = false;
  announceEl.className = 'announce ' + announcement.level;
  announceMsg.textContent = announcement.message;
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
    // three tappable examples. The old four-paragraph pitch described the
    // product instead of telling the reader what to do.
    empty.innerHTML = MARK_SVG +
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

topicSelect.addEventListener('change', () => {
  drafts.set(sharedThreadId || '', q.value);
  vscode.postMessage({ type: 'sharedSelect', threadId: topicSelect.value || null });
});
document.getElementById('topic-new').addEventListener('click', () => {
  document.getElementById('topic-form').hidden = false;
  document.getElementById('topic-title').focus();
});
document.getElementById('topic-cancel').addEventListener('click', () => {
  document.getElementById('topic-form').hidden = true;
  document.getElementById('topic-new').focus();
});
document.getElementById('topic-form').addEventListener('submit', (event) => {
  event.preventDefault();
  const title = document.getElementById('topic-title').value.trim();
  if (title && !sharedBusy) vscode.postMessage({ type: 'sharedCreate', title });
});
document.getElementById('topic-refresh').addEventListener('click', () => vscode.postMessage({ type: 'sharedRefresh' }));

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
    }
    const nextTopics = JSON.stringify((msg.threads || []).map(t => [t.id, t.title]));
    if (nextTopics !== topicSignature) {
      topicSignature = nextTopics;
      topicSelect.replaceChildren();
      const local = document.createElement('option');
      local.value = '';
      local.textContent = 'Local knowledge console';
      topicSelect.appendChild(local);
      for (const thread of msg.threads || []) {
        const option = document.createElement('option');
        option.value = thread.id;
        option.textContent = thread.title;
        topicSelect.appendChild(option);
      }
    }
    topicSelect.value = sharedThreadId || '';
    sharedBusy = !!msg.busy;
    sendBtn.disabled = !!sharedThreadId && sharedBusy;
    document.getElementById('topic-new').disabled = sharedBusy;
    topicStatus.textContent = msg.problem || (sharedThreadId ? 'Shared through Cinder · library Q&A via the Cinder provider' : 'Shared topics · choose a topic or create one');
    topicStatus.className = msg.problem ? 'error' : '';
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
    announcement = { level: msg.level || 'info', message: msg.message || '' };
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
  }
});

setMode('ask');
renderScope();
renderFeed();
// The host answers with the init message — a push before this script ran
// would be dropped, and the panel would come up empty.
vscode.postMessage({ type: 'ready' });
</script>
</body>
</html>`;
}
