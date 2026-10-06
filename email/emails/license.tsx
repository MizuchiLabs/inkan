import {
	Body,
	Column,
	Container,
	Head,
	Heading,
	Hr,
	Html,
	Img,
	Link,
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
};

// Yours to change. An empty one leaves its part out.
const logo = '';
const support = 'support@example.com';
const company = 'Example GmbH, Street 1, 12345 Berlin';

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
				page: '#f3f4f8'
			}
		}
	}
};

export default function License({ licenseKey, name, plan, email, expires }: Props) {
	return (
		<Tailwind config={theme}>
			<Html lang="en">
				<Head>
					<title>{`Your ${name} license key`}</title>
				</Head>
				<Preview>{`Your ${plan} license key for ${name}, valid until ${expires}`}</Preview>
				<Body className="m-0 bg-page font-sans">
					<Container className="mx-auto max-w-140 px-3 py-8">
						<Section className="rounded-t-[10px] bg-band px-8 py-5">
							{logo ? (
								<Img src={logo} alt={name} height={32} className="block" />
							) : (
								<Text className="m-0 text-[20px] leading-8 font-bold text-white">{name}</Text>
							)}
						</Section>

						<Section className="border-x border-y-0 border-solid border-line bg-white px-8 pt-8 pb-7">
							<Text className="m-0 mb-1.5 text-[12px] leading-4 font-bold tracking-[1.2px] text-accent uppercase">
								{plan} license
							</Text>
							<Heading as="h1" className="m-0 text-[28px] leading-8 font-bold text-ink">
								Your license key
							</Heading>
							<Text className="mt-4 mb-6 text-[16px] leading-6 text-soft">
								Paste the key into {name} to activate it. Keep this mail in case you need the key
								again.
							</Text>

							<Section className="rounded-lg border border-solid border-accent-line bg-accent-soft px-5 pt-4 pb-5">
								<Text className="m-0 mb-2 text-[11px] leading-4 font-bold tracking-[1.2px] text-soft uppercase">
									License key
								</Text>
								<Text className="m-0 font-mono text-[14px] leading-5.5 font-semibold break-all text-ink">
									{licenseKey}
								</Text>
							</Section>

							<Row className="mt-3">
								<Column className="py-3 text-[14px] leading-5 whitespace-nowrap text-soft">
									Licensed to
								</Column>
								<Column
									align="right"
									className="py-3 pl-4 text-[14px] leading-5 font-semibold break-all text-ink"
								>
									{email}
								</Column>
							</Row>
							<Hr className="m-0 border-line" />
							<Row>
								<Column className="pt-3 text-[14px] leading-5 whitespace-nowrap text-soft">
									Valid until
								</Column>
								<Column
									align="right"
									className="pt-3 pl-4 text-[14px] leading-5 font-semibold text-ink"
								>
									{expires}
								</Column>
							</Row>

							{support && (
								<>
									<Hr className="mt-3 mb-0 border-line" />
									<Text className="mt-5 mb-0 text-[15px] leading-5 text-soft">
										Questions? Write to{' '}
										<Link href={`mailto:${support}`} className="text-accent underline">
											{support}
										</Link>
										.
									</Text>
								</>
							)}
						</Section>

						<Section className="rounded-b-[10px] border border-solid border-line bg-[#f8f9fc] px-8 pt-5 pb-5.5">
							<Text className="m-0 text-[13px] leading-5 text-soft">
								You are getting this mail because a license for {name} was paid for with this
								address.
							</Text>
							{company && (
								<Text className="mt-2.5 mb-0 text-[12px] leading-5 text-soft">{company}</Text>
							)}
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
	expires: '16 October 2096'
} satisfies Props;
