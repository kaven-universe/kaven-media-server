/********************************************************************
 * @author:      Kaven
 * @email:       kaven@wuwenkai.com
 * @website:     http://blog.kaven.xyz
 * @file:        [kaven-image] /postcss.config.js
 * @create:      2025-09-08 16:47:13.549
 * @modify:      2025-09-08 16:47:13.549
 * @version:     0.0.2
 * @times:       1
 * @lines:       27
 * @copyright:   Copyright © 2025 Kaven. All Rights Reserved.
 * @description: [description]
 * @license:     [license]
 ********************************************************************/

import autoprefixer from "autoprefixer";
// import rtlcss from 'postcss-rtlcss'

export default {
    plugins: [
    // https://github.com/postcss/autoprefixer
        autoprefixer({
            overrideBrowserslist: [
                "last 4 Chrome versions",
                "last 4 Firefox versions",
                "last 4 Edge versions",
                "last 4 Safari versions",
                "last 4 Android versions",
                "last 4 ChromeAndroid versions",
                "last 4 FirefoxAndroid versions",
                "last 4 iOS versions",
            ],
        }),

    // https://github.com/elchininet/postcss-rtlcss
    // If you want to support RTL css, then
    // 1. yarn/pnpm/bun/npm install postcss-rtlcss
    // 2. optionally set quasar.config.js > framework > lang to an RTL language
    // 3. uncomment the following line (and its import statement above):
    // rtlcss()
    ],
};