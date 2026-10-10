import {
	Body,
	Column,
	Container,
	Head,
	Heading,
	Hr,
	Html,
	Img,
	pixelBasedPreset,
	Preview,
	Row,
	Section,
	Tailwind,
	Text
} from 'react-email';

type Props = {
	licenseKey: string;
	name: string;
	plan: string;
	email: string;
	// A date like 16 October 2096, or Never.
	expires: string;
	// The footer and company details of the product in the dashboard.
	footer: string;
	company: string;
};

// Yours to change. An https url, empty shows the name instead.
const logo = '';

// The colors of the mail. The dark ones apply in a mail client in dark mode.
const theme = {
	presets: [pixelBasedPreset],
	theme: {
		extend: {
			colors: {
				accent: '#2f3e8f',
				'accent-soft': '#eff0f7',
				'accent-line': '#c9cde3',
				band: '#1a1d2b',
				ink: '#1c1f2e',
				soft: '#5b6072',
				line: '#dcdfe8',
				page: '#f3f4f8',
				foot: '#f8f9fc',
				'accent-dark': '#979fc7',
				'accent-soft-dark': '#1d2239',
				'accent-line-dark': '#222a52',
				'ink-dark': '#eceef4',
				'soft-dark': '#a4a9ba',
				'line-dark': '#2c3040',
				'page-dark': '#10121a',
				'card-dark': '#1a1d29',
				'foot-dark': '#151823'
			}
		}
	}
};

export default function License({ licenseKey, name, plan, email, expires, footer, company }: Props) {
	return (
		<Tailwind config={theme}>
			<Html lang="en">
				<Head>
					<title>{`Your ${name} license key`}</title>
					<meta name="color-scheme" content="light dark" />
					<meta name="supported-color-schemes" content="light dark" />
				</Head>
				<Preview>{`Your ${plan} license key for ${name}, valid until ${expires}`}</Preview>
				<Body className="m-0 bg-page font-sans dark:bg-page-dark">
					<Container className="mx-auto max-w-[560px] px-3 py-8">
						<Section className="rounded-t-[10px] bg-band px-5 py-5 sm:px-8">
							{logo ? (
								<Img src={logo} alt={name} height={32} className="block" />
							) : (
								<Text className="m-0 text-[20px] leading-8 font-bold text-white">{name}</Text>
							)}
						</Section>

						<Section className="border-x border-y-0 border-solid border-line bg-white px-5 pt-8 pb-7 sm:px-8 dark:border-line-dark dark:bg-card-dark">
							<Text className="m-0 mb-1.5 text-[12px] leading-4 font-bold tracking-[1.2px] text-accent uppercase dark:text-accent-dark">
								{plan} license
							</Text>
							<Heading
								as="h1"
								className="m-0 text-[24px] leading-[30px] font-bold text-ink sm:text-[28px] sm:leading-8 dark:text-ink-dark"
							>
								Your license key
							</Heading>
							<Text className="mt-4 mb-6 text-[16px] leading-6 text-soft dark:text-soft-dark">
								Paste the key into {name} to activate it. Keep this mail in case you need the key
								again.
							</Text>

							<Section className="rounded-[8px] border border-solid border-accent-line bg-accent-soft px-5 pt-4 pb-5 dark:border-accent-line-dark dark:bg-accent-soft-dark">
								<Text className="m-0 mb-2 text-[11px] leading-4 font-bold tracking-[1.2px] text-soft uppercase dark:text-soft-dark">
									License key
								</Text>
								<Text className="m-0 font-mono text-[14px] leading-[22px] font-semibold break-all text-ink dark:text-ink-dark">
									{licenseKey}
								</Text>
							</Section>

							<Row className="mt-3">
								<Column className="py-3 text-[14px] leading-5 whitespace-nowrap text-soft dark:text-soft-dark">
									Licensed to
								</Column>
								<Column
									align="right"
									className="py-3 pl-4 text-[14px] leading-5 font-semibold break-all text-ink dark:text-ink-dark"
								>
									{email}
								</Column>
							</Row>
							<Hr className="m-0 border-t-line dark:border-t-line-dark" />
							<Row>
								<Column className="pt-3 text-[14px] leading-5 whitespace-nowrap text-soft dark:text-soft-dark">
									Valid until
								</Column>
								<Column
									align="right"
									className="pt-3 pl-4 text-[14px] leading-5 font-semibold text-ink dark:text-ink-dark"
								>
									{expires}
								</Column>
							</Row>

							<Text className="mt-6 mb-0 text-[15px] leading-[23px] text-soft dark:text-soft-dark">
								{footer}
							</Text>
						</Section>

						<Section className="rounded-b-[10px] border border-solid border-line bg-foot px-5 pt-5 pb-[22px] sm:px-8 dark:border-line-dark dark:bg-foot-dark">
							<Text className="m-0 text-[13px] leading-5 text-soft dark:text-soft-dark">
								You are getting this mail because a license for {name} was paid for with this
								address.
							</Text>
							<Text className="mt-2.5 mb-0 text-[12px] leading-5 text-soft dark:text-soft-dark">
								{company}
							</Text>
						</Section>
					</Container>
				</Body>
			</Html>
		</Tailwind>
	);
}

// What `pnpm dev` shows. The build puts the placeholders of the worker here.
License.PreviewProps = {
	licenseKey:
		'MYAPP-AXsidGVzdCI6InN1Yl8xUGFiY2RlZmdoaWprbG1ub3AiLCJwbGFuIjoicHJvIiwiZW1haWwiOiJjdXN0b21lckBleGFtcGxlLmNvbSJ9-8Zk3vQeX1mN7cR2pLw9TfYbHs4JdUa6GoKi0EtVxWnBqCzMlPyDrSgFhJuIkAo5',
	name: 'My App',
	plan: 'pro',
	email: 'customer@example.com',
	expires: '16 October 2096',
	footer: 'Questions? Write to support@example.com',
	company: 'Example GmbH, Street 1, 12345 Berlin, HRB 12345'
} satisfies Props;
