import clsx from 'clsx';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import HomepageFeatures from '@site/src/components/HomepageFeatures';
import Typewriter from 'typewriter-effect';

import Heading from '@theme/Heading';
import styles from './index.module.css';
import Hero from '@site/static/img/Hero.png';

function HomepageHeader() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <header className={clsx('hero hero--primary', styles.heroBanner)}>
      <div className="container">
        <Heading as="h1" className="hero__title">
          {siteConfig.title} deploys 
          <Typewriter
            options={{
              strings: ['Active Directory', 'ADCS', 'Kali', 'SCCM', 'any Ansible role', 'your next research target'],
              autoStart: true,
              loop: true,
              delay: 60,
            }}
          />
        </Heading>
        <p className="hero__subtitle">{siteConfig.tagline}
        
        
        </p>
        <div className={styles.buttons}>
          <Link
            className="button button--secondary button--lg"
            to="docs/category/quick-start">
            Ludus Quick Start
          </Link>
        </div>
        <p className="container">
          <img src={Hero} />
        </p>
      </div> 
    </header>
  );
}

export default function Home() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <Layout
      title={`${siteConfig.title}`}
      description="Documentation for the Ludus cyber ranges project">
      <HomepageHeader />
      <main>
        <HomepageFeatures />
      </main>
    </Layout>
  );
}
