// @ts-check
// `@type` JSDoc annotations allow editor autocompletion and type checking
// (when paired with `@ts-check`).
// There are various equivalent ways to declare your Docusaurus config.
// See: https://docusaurus.io/docs/api/docusaurus-config

import {themes as prismThemes} from 'prism-react-renderer';

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'Ludus',
  tagline: 'Cyber Ranges for Everyone',
  favicon: 'img/favicon.ico',

  // Set the production url of your site here
  url: 'https://ludus.cloud',
  // Set the /<baseUrl>/ pathname under which your site is served
  // For GitHub pages deployment, it is often '/<projectName>/'
  baseUrl: '/ludus/',

  // GitHub pages deployment config.
  // If you aren't using GitHub pages, you don't need these.
  organizationName: 'Ludus Authors', // Usually your GitHub org/user name.
  projectName: 'ludus', // Usually your repo name.

  onBrokenLinks: 'throw',
  onBrokenMarkdownLinks: 'warn',


  // Even if you don't use internationalization, you can use this field to set
  // useful metadata like html lang. For example, if your site is Chinese, you
  // may want to replace "en" with "zh-Hans".
  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  themes: ["docusaurus-json-schema-plugin"],

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          sidebarPath: './sidebars.js',
          // Please change this to your repo.
          // Remove this to remove the "edit this page" links.
         // editUrl:
          //  'https://github.com/facebook/docusaurus/tree/main/packages/create-docusaurus/templates/shared/',
        },
        blog: {
          showReadingTime: true,
          // Please change this to your repo.
          // Remove this to remove the "edit this page" links.
          //editUrl:
          //  'https://github.com/facebook/docusaurus/tree/main/packages/create-docusaurus/templates/shared/',
        },
        theme: {
          customCss: './src/css/custom.css',
        },
      }),
    ],
  ],

  themeConfig:
    /** @type {import('@docusaurus/preset-classic').ThemeConfig} */
    ({
      // Replace with your project's social card
      image: 'img/ludus-social-card.jpg',
      colorMode: {
        defaultMode: 'dark',
        disableSwitch: false,
        respectPrefersColorScheme: false,
      },
      algolia: {
        // The application ID provided by Algolia
        appId: 'N1Z8B4158Z',
        // Public API key: it is safe to commit it
        apiKey: 'ec0d92a3a2e50ebd46a983c4a52486f0',
        indexName: 'ludus',
        contextualSearch: false,
        searchParameters: {
            facetFilters: []
        },
        // Optional: path for search page that enabled by default (`false` to disable it)
        searchPagePath: 'search',
      },
      navbar: {
        title: 'Ludus',
        logo: {
          alt: 'Ludus Logo',
          src: 'img/logo.svg',
        },
        items: [
          {
            label: 'Quick Start',
            position: 'left',
            to: 'docs/category/quick-start'
          },
          {
            label: 'Docs', 
            position: 'left',
            to: 'docs/intro'
          },
          {
            label: 'API',
            position: 'left',
            href: 'pathname:///api/index.html'
          },
          {
            label: 'GitLab',
            position: 'right',
            href: 'https://gitlab.com/badsectorlabs/ludus'
          },
        ],
      },
      footer: {
        style: 'dark',
        links: [
          {
            title: 'Docs',
            items: [
              {
                label: 'Quick Start',
                to: '/docs/category/quick-start',
              },
              {
                label: 'Docs',
                to: '/docs/intro',
              },
            ],
          },
          {
            title: 'Community',
            items: [
            //   {
            //     label: 'Stack Overflow',
            //     href: 'https://stackoverflow.com/questions/tagged/docusaurus',
            //   },
              {
                label: 'Discord',
                href: 'https://discord.gg/HryzhdUSYT',
              },
            //   {
            //     label: 'Twitter',
            //     href: 'https://twitter.com/docusaurus',
            //   },
            ],
          },
          {
            title: 'More',
            items: [
              {
                label: 'Gitlab',
                href: 'https://gitlab.com/badsectorlabs/ludus',
              },
            ],
          },
        ],
        copyright: `Copyright © ${new Date().getFullYear()} Bad Sector Labs`,
      },
      prism: {
        theme: prismThemes.github,
        darkTheme: prismThemes.dracula,
        additionalLanguages: ['bash', 'powershell', 'yaml', 'shell-session', 'ini'],
        magicComments: [
          // Remember to extend the default highlight class name as well!
          {
            className: 'theme-code-block-highlighted-line',
            line: 'highlight-next-line',
            block: {start: 'highlight-start', end: 'highlight-end'},
          },
          {
            className: 'code-block-terminal-command-local',
            line: 'terminal-command-local',
          },
          {
            className: 'code-block-terminal-command-ludus',
            line: 'terminal-command-ludus',
          },
          {
            className: 'code-block-terminal-command-user1',
            line: 'terminal-command-user1',
          },
          {
            className: 'code-block-terminal-command-user-at-debian',
            line: 'terminal-command-user-at-debian',
          },
          {
            className: 'code-block-terminal-command-root-at-debian',
            line: 'terminal-command-root-at-debian',
          },          
          {
            className: 'code-block-terminal-command-powershell',
            line: 'terminal-command-powershell',
          },
          {
            className: 'code-block-terminal-command-ludus-root',
            line: 'terminal-command-ludus-root',
          },
          {
            className: 'code-block-terminal-command-goad',
            line: 'terminal-command-goad',
          },
        ],
      },
    }),
};

export default config;
