import clsx from 'clsx';
import Heading from '@theme/Heading';
import styles from './styles.module.css';

const FeatureList = [
  {
    title: 'Easy to Use',
    Svg: require('@site/static/img/undraw_code_review.svg').default,
    description: (
      <>
        Ludus was designed from the ground up to be easily installed and
        used to get your cyber ranges up and running quickly.
      </>
    ),
  },
  {
    title: 'Focus on What Matters',
    Svg: require('@site/static/img/undraw_developer_activity.svg').default,
    description: (
      <>
        Ludus lets you focus on your testing, and we&apos;ll handle the tedious tasks and operational security.
      </>
    ),
  },
  {
    title: 'Powered by Proxmox, Packer, and Ansible',
    Svg: require('@site/static/img/undraw_server_cluster.svg').default,
    description: (
      <>
        Extend or customize your cyber ranges using modules. Ludus can
        be extended while reusing included templates, roles, and tasks.
      </>
    ),
  },
];

function Feature({Svg, title, description}) {
  return (
    <div className={clsx('col col--4')}>
      <div className="text--center">
        <Svg className={styles.featureSvg} role="img" />
      </div>
      <div className="text--center padding-horiz--md">
        <Heading as="h3">{title}</Heading>
        <p>{description}</p>
      </div>
    </div>
  );
}

export default function HomepageFeatures() {
  return (
    <section className={styles.features}>
      <div className="container">
        <div className="row">
          {FeatureList.map((props, idx) => (
            <Feature key={idx} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}
