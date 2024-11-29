module.exports = {
    root: true,
    env: {
        browser: true,
        node: true,
    },
    extends: [
        "eslint:recommended",
        "plugin:@typescript-eslint/recommended",
        "prettier",
    ],
    parser: "@typescript-eslint/parser",
    plugins: ["@typescript-eslint", "jsdoc"],
    rules: {
        "no-constant-condition": [
            "error",
            {
                checkLoops: false,
            },
        ],
    }
};
